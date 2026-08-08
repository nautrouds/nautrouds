package rtree

import (
	"bytes"
	"log"
	"nautrouds/internal/interpolate"
	"nautrouds/internal/tags"
	"regexp"
	"slices"
	"strings"
)

type RawNode struct {
	URL         string
	Service     string
	Middlewares []string
	Methods     string // Comma-separated methods, e.g., "GET,POST"
	Tags        []string
}

type buildEdge struct {
	Fragment []byte
	Node     *buildNode
}

type buildNode struct {
	Edges       []buildEdge
	ActionIndex uint32
	Methods     uint16
	Tags        uint16
}

func Build(rawNodes []*RawNode) *RouteTree {
	if len(rawNodes) == 0 {
		log.Println("[rtree] Build failed: no raw nodes provided")
		return nil
	}

	t := &RouteTree{
		NodePool: make([]RouteNode, 0, len(rawNodes)),
	}
	roots := make([]buildEdge, 256)

	actionMap := make(map[string]uint32)
	wildcardReg := regexp.MustCompile(`\*{2,}`)

	for _, raw := range rawNodes {
		url := wildcardReg.ReplaceAll([]byte(raw.URL), []byte{WildcardGreedy})
		url = bytes.ReplaceAll(url, []byte("*"), []byte{Wildcard})
		ReverseHost(url)
		methodMask := parseMethods(raw.Methods)
		tagMask := tags.Analyze(raw.Tags)

		svcID := t.getOrCreateActionID(raw.Service, actionMap)

		mwIDs := make([]uint32, len(raw.Middlewares))
		for i, mw := range raw.Middlewares {
			mwIDs[i] = t.getOrCreateActionID(mw, actionMap)
		}
		actionIndex := uint32(len(t.ActionMetadata))

		mwCount := len(mwIDs)

		actions := make([]uint32, 2, mwCount+2)
		actions[0] = svcID
		actions[1] = uint32(mwCount)
		actions = append(actions, mwIDs...)

		t.ActionMetadata = append(t.ActionMetadata, actions...)

		insert(roots, url, actionIndex, methodMask, tagMask)
	}

	totalLen := compress(roots)
	t.finalize(roots, totalLen)

	log.Printf("[rtree] Successfully built tree with %d nodes", len(t.NodePool))
	return t
}

func (t *RouteTree) getOrCreateActionID(action string, actionMap map[string]uint32) uint32 {
	actionID, exists := actionMap[action]
	if !exists {
		id := uint32(len(t.ActionsRegistry))
		actionBytes := []byte(action)
		actionLen := len(actionBytes)

		actionChunk := make([]byte, 0, actionLen+((actionLen+254)/255))
		for actionLen > 0 {
			l := min(actionLen, 255)
			actionChunk = append(actionChunk, byte(l))
			if actionLen == 255 {
				actionChunk = append(actionChunk, 0)
				break
			}
			actionLen -= l
		}
		actionChunk = append(actionChunk, actionBytes...)
		t.ActionsRegistry = append(t.ActionsRegistry, actionChunk...)

		actionID = uint32(len(t.ActionMetadata))

		ops := interpolate.Analyze(action)

		l := len(ops)
		actionMetadata := make([]uint32, 2, l+2)
		actionMetadata[0] = id
		actionMetadata[1] = uint32(l)
		actionMetadata = append(actionMetadata, ops...)
		t.ActionMetadata = append(t.ActionMetadata, actionMetadata...)

		actionMap[action] = actionID
	}
	return actionID
}

func insert(roots []buildEdge, url []byte, actionIndex uint32, methods uint16, tags uint16) {
	if len(url) == 0 {
		return
	}

	firstChar := url[0]
	edge := &roots[firstChar]
	if edge.Node == nil {
		edge.Fragment = []byte{firstChar}
		edge.Node = &buildNode{}
	}

	currNode := edge.Node
	for i := 1; i < len(url); i++ {
		char := url[i]
		var nextEdge *buildEdge
		for j := range currNode.Edges {
			if currNode.Edges[j].Fragment[0] == char {
				nextEdge = &currNode.Edges[j]
				break
			}
		}

		if nextEdge == nil {
			currNode.Edges = append(currNode.Edges, buildEdge{
				Node:     &buildNode{},
				Fragment: []byte{char},
			})
			nextEdge = &currNode.Edges[len(currNode.Edges)-1]
		}
		currNode = nextEdge.Node
	}

	currNode.ActionIndex = actionIndex
	currNode.Methods = methods
	currNode.Tags = tags
}

func compress(roots []buildEdge) int {
	totalLen := 0
	for i := range 256 {
		if roots[i].Node != nil {
			totalLen += compressNode(roots[i].Node)
		}
	}
	return totalLen
}

func compressEdge(e *buildEdge) (*buildEdge, int, bool) {
	if e.Node == nil {
		return nil, 0, false
	}

	isWildcard := e.Fragment[0] == Wildcard || e.Fragment[0] == WildcardGreedy
	switch len(e.Node.Edges) {
	case 0:
		if isWildcard {
			return e, 0, false
		}
		return e, 1, true
	case 1:
		child, l, ok := compressEdge(&e.Node.Edges[0])
		if isWildcard {
			return e, l, false
		}
		if ok && e.Node.Methods == 0 {
			e.Fragment = append(e.Fragment, child.Fragment...)
			e.Node = child.Node
		}
		return e, (l + 1), true
	default:
		l := compressNode(e.Node)
		return e, (l + 1), true
	}
}

func compressNode(n *buildNode) int {
	total := 0
	for i := range n.Edges {
		_, l, _ := compressEdge(&n.Edges[i])
		total += l
	}
	// Ensure wildcard is always the last edge for searching priority
	slices.SortFunc(n.Edges, func(a, b buildEdge) int {
		typeOf := func(f []byte) int {
			if len(f) == 0 {
				return 0
			}
			switch f[0] {
			case WildcardGreedy:
				return 2
			case Wildcard:
				return 1
			default:
				return 0
			}
		}
		return typeOf(a.Fragment) - typeOf(b.Fragment)
	})
	return total
}

func (t *RouteTree) finalize(roots []buildEdge, estimatedLen int) {
	t.NodePool = make([]RouteNode, 1) // 0 index is reserved/null
	t.EdgePool = make([]Edge, 256)
	t.FragmentPool = make([]byte, 2, 2+estimatedLen)
	t.FragmentPool[0] = WildcardGreedy
	t.FragmentPool[1] = Wildcard

	var wildcardEdge *Edge
	for i := range 256 {
		if roots[i].Node != nil {
			isWildcard := roots[i].Fragment[0] == Wildcard || roots[i].Fragment[0] == WildcardGreedy

			var wildcardIdx int32
			if isWildcard {
				wildcardIdx++
			}

			edge := t.flattenNode(&roots[i], wildcardEdge, wildcardIdx)
			t.EdgePool[i] = edge

			if isWildcard {
				wildcardEdge = &Edge{
					TargetID: edge.TargetID,
					Offset:   edge.Offset,
					End:      edge.End,
				}
			}
		}
	}
}

func (t *RouteTree) flattenNode(e *buildEdge, pw *Edge, pwIdx int32) Edge {
	firstChar := e.Fragment[0]
	isCurrentWildcard := (firstChar == Wildcard || firstChar == WildcardGreedy)

	var wildcardEdge *Edge
	wildcardIndex := pwIdx
	if pw != nil {
		edge := &Edge{
			TargetID:      pw.TargetID,
			Offset:        pw.Offset,
			End:           pw.End,
			GraftRollback: pw.GraftRollback,
		}
		if !isCurrentWildcard {
			edge.GraftRollback += int32(len(e.Fragment))
		}
		wildcardEdge = edge
	}

	edges := make([]Edge, 0, len(e.Node.Edges)+1)
	for i := len(e.Node.Edges) - 1; i >= 0; i-- {
		edge := t.flattenNode(&e.Node.Edges[i], wildcardEdge, wildcardIndex)

		switch t.FragmentPool[edge.Offset] {
		case Wildcard, WildcardGreedy:
			wildcardEdge = &Edge{
				TargetID: edge.TargetID,
				Offset:   edge.Offset,
				End:      edge.End,
			}
			if isCurrentWildcard {
				if wildcardIndex == pwIdx {
					wildcardIndex++
				}
				wildcardEdge.GraftRollback = 0 - wildcardIndex
			}
		}
		edges = append(edges, edge)
	}

	if len(edges) > 0 {
		slices.Reverse(edges)
		if pw != nil {
			edges = append(edges, *pw)
		}
	}

	edgeOffset := uint16(len(t.EdgePool))
	t.EdgePool = append(t.EdgePool, edges...)
	edgeCount := uint16(len(edges))

	var offset uint32
	var end uint32
	if isCurrentWildcard {
		offset = uint32(e.Fragment[0]) - 1
		end = offset + 1
	} else {
		offset = uint32(len(t.FragmentPool))
		t.FragmentPool = append(t.FragmentPool, e.Fragment...)
		end = uint32(len(t.FragmentPool))
	}

	nodeID := uint32(len(t.NodePool))
	t.NodePool = append(t.NodePool, RouteNode{
		EdgeOffset:  edgeOffset,
		EdgeCount:   edgeCount,
		ActionIndex: e.Node.ActionIndex,
		Methods:     e.Node.Methods,
		Tags:        e.Node.Tags,
	})

	return Edge{
		TargetID: nodeID,
		Offset:   offset,
		End:      end,
	}
}

func parseMethods(methods string) uint16 {
	var res uint16
	for m := range strings.SplitSeq(strings.ToLower(methods), ",") {
		token := strings.TrimSpace(m)
		mask, ok := lookupMethodToken(token)
		if !ok && token != "" {
			log.Printf("[rtree] Warning: unknown HTTP method token ignored: %s", token)
		}
		res |= mask
	}
	return res
}

func ValidateMethods(methods string) (bool, string) {
	for m := range strings.SplitSeq(methods, ",") {
		token := strings.TrimSpace(m)
		if token == "" {
			continue
		}
		if _, ok := lookupMethodToken(strings.ToLower(token)); !ok {
			return false, token
		}
	}
	return true, ""
}

func lookupMethodToken(m string) (uint16, bool) {
	switch m {
	case "g", "get":
		return MethodGet, true
	case "p", "po", "post":
		return MethodPost, true
	case "pu", "put":
		return MethodPut, true
	case "d", "del", "delete":
		return MethodDelete, true
	case "head":
		return MethodHead, true
	case "connect":
		return MethodConnect, true
	case "options":
		return MethodOptions, true
	case "trace":
		return MethodTrace, true
	case "patch":
		return MethodPatch, true
	case "*", "any":
		return MethodAny, true
	default:
		return 0, false
	}
}
