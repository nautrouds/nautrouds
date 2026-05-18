package rtree

import (
	"bytes"
	"fmt"
	"log"
	"nautrouds/internal/interpolate"
	"nautrouds/internal/tags"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unsafe"
)

const (
	MethodGet uint16 = 1 << iota
	MethodPost
	MethodPut
	MethodDelete
	MethodHead
	MethodConnect
	MethodOptions
	MethodTrace
	MethodPatch
	MethodAny uint16 = 0xFFF
)

const (
	WildcardGreedy byte = 0x01
	Wildcard       byte = 0x02
)

// HTTPMethodMap maps standard HTTP method strings to internal bitmasks.
var HTTPMethodMap = map[string]uint16{
	http.MethodGet:     MethodGet,
	http.MethodPost:    MethodPost,
	http.MethodPut:     MethodPut,
	http.MethodDelete:  MethodDelete,
	http.MethodHead:    MethodHead,
	http.MethodConnect: MethodConnect,
	http.MethodOptions: MethodOptions,
	http.MethodTrace:   MethodTrace,
	http.MethodPatch:   MethodPatch,
}

// Edge represents the transition from a parent node to a child node.
type Edge struct {
	Fragment *[]byte    // Raw fragment used during tree construction
	Node     *RouteNode // Temporary pointer used during construction
	TargetID uint32     // Index of the destination node in NodePool (finalized)
	Offset   uint32     // Start position of the fragment in FragmentPool
	End      uint32     // End position of the fragment in FragmentPool

	// GraftRollback manages cursor adjustments during wildcard backtracking.
	// If > 0: specifies the absolute number of bytes to roll back the URL cursor.
	// If < 0: represents a bitwise NOT index (^GraftRollback) to retrieve the
	// saved cursor state from cursorStack.
	GraftRollback int32
}

// RouteTree is the primary data structure for route indexing and searching.
type RouteTree struct {
	FragmentPool    []byte // Contiguous memory for all path fragments
	ActionsRegistry []byte // Registry of middleware & service identifiers
	ActionMetadata  []uint32
	NodePool        []RouteNode // Flattened node storage for cache locality
	EdgePool        []Edge      // Flattened edge storage for cache locality
}

// RouteNode represents a specific point in the routing tree.
type RouteNode struct {
	Edges       *[]Edge // Outgoing transitions
	ActionIndex uint32
	EdgeOffset  uint16
	EdgeCount   uint16
	Methods     uint16 // Bitmask of allowed HTTP methods; 0 if not a leaf
	Tags        uint16
}

// Search looks up a URL in the tree and returns the matching RouteNode.
// Returns (node, true) if found, (nil, false) otherwise.
func (t *RouteTree) Search(url []byte) (*RouteNode, bool) {
	urlLen := int32(len(url))
	if urlLen == 0 {
		return nil, false
	}

	firstChar := url[0]
	var currentEdgeIdx uint16
	var buf [16]int32
	cursorStack := buf[:0]
	cursor := int32(0)

	switch {
	case t.EdgePool[firstChar].TargetID != 0:
		currentEdgeIdx = uint16(firstChar)
		cursor++
	case t.EdgePool[Wildcard].TargetID != 0:
		currentEdgeIdx = uint16(Wildcard)
		cursorStack = append(cursorStack, 0)
	case t.EdgePool[WildcardGreedy].TargetID != 0:
		currentEdgeIdx = uint16(WildcardGreedy)
		cursorStack = append(cursorStack, 0)
	default:
		return nil, false
	}

	for {
		edge := &t.EdgePool[currentEdgeIdx]
		node := &t.NodePool[edge.TargetID]

		switch t.FragmentPool[edge.Offset] {
		case Wildcard:
			if edge.GraftRollback < 0 {
				if node.EdgeCount == 0 {
					cursorStack = cursorStack[:^edge.GraftRollback]
				} else {
					childEdge := &t.EdgePool[node.EdgeOffset+node.EdgeCount-1]
					if t.FragmentPool[childEdge.Offset] != WildcardGreedy || childEdge.GraftRollback != edge.GraftRollback {
						cursorStack = cursorStack[:^edge.GraftRollback]
					}
				}
			}
		case WildcardGreedy:
			if edge.GraftRollback < 0 {
				cursorStack = cursorStack[:^edge.GraftRollback]
			}
		default:
			if cursor == urlLen && node.Methods != 0 {
				return node, true
			}
		}

		matched := false

	INNER:
		for i := range node.EdgeCount {
			childEdgeIdx := node.EdgeOffset + i
			childEdge := &t.EdgePool[childEdgeIdx]
			childFrag := t.FragmentPool[childEdge.Offset:childEdge.End]
			childFragLen := int32(len(childFrag))

			switch t.FragmentPool[childEdge.Offset] {
			case Wildcard, WildcardGreedy:
				switch {
				case childEdge.GraftRollback > 0:
					cursor -= childEdge.GraftRollback
				case childEdge.GraftRollback < 0:
					cursor = cursorStack[^childEdge.GraftRollback]
				default:
					cursorStack = append(cursorStack, cursor)
				}
				currentEdgeIdx = childEdgeIdx
				matched = true
				break INNER
			default:
				parentFirstChar := t.FragmentPool[edge.Offset]
				switch parentFirstChar {
				case Wildcard:
					slashIdx := slices.Index(url[cursor:], '/')
					foundIdx := bytes.Index(url[cursor:], childFrag)

					if foundIdx != -1 && (slashIdx == -1 || foundIdx <= slashIdx) {
						cursor += (int32(foundIdx) + childFragLen)
						currentEdgeIdx = childEdgeIdx
						matched = true
						break INNER
					}
				case WildcardGreedy:
					foundIdx := bytes.Index(url[cursor:], childFrag)

					if foundIdx != -1 {
						cursor += (int32(foundIdx) + childFragLen)
						currentEdgeIdx = childEdgeIdx
						matched = true
						break INNER
					}
				default:
					isMatch := urlLen >= cursor+childFragLen && bytes.Equal(url[cursor:cursor+childFragLen], childFrag)

					if isMatch {
						cursor += childFragLen
						currentEdgeIdx = childEdgeIdx
						matched = true
						break INNER
					}
				}
			}
		}

		if t.FragmentPool[edge.Offset] < 3 && cursor < urlLen && node.Methods != 0 {
			return node, true
		}

		if !matched {
			return nil, false
		}
	}
}

// RawNode represents the input format for building a RouteTree.
type RawNode struct {
	URL         string
	Service     string
	Middlewares []string
	Methods     string // Comma-separated methods, e.g., "GET,POST"
	Tags        []string
}

// Build constructs a finalized RouteTree from a slice of RawNodes.
// Logs an error and returns nil if the input is empty.
func Build(rawNodes []*RawNode) *RouteTree {
	if len(rawNodes) == 0 {
		log.Println("[rtree] Build failed: no raw nodes provided")
		return nil
	}

	t := &RouteTree{
		NodePool: make([]RouteNode, 0, len(rawNodes)),
		EdgePool: make([]Edge, 256),
	}

	actionMap := make(map[string]uint32)
	wildcardReg := regexp.MustCompile(`\*{2,}`)

	for _, raw := range rawNodes {
		url := wildcardReg.ReplaceAll([]byte(raw.URL), []byte{WildcardGreedy})
		url = bytes.ReplaceAll(url, []byte("*"), []byte{Wildcard})
		url = ReverseHost(url)
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

		t.insert(url, actionIndex, methodMask, tagMask)
	}

	totalLen := t.compress()
	t.finalize(totalLen)

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

func (t *RouteTree) GetActionName(index uint32) string {
	regLen := uint32(len(t.ActionsRegistry))
	if index >= regLen {
		return ""
	}

	curr := index
	length := uint32(0)

	for {
		l := t.ActionsRegistry[curr]
		length += uint32(l)
		curr++
		if l < 255 {
			break
		}
	}

	start := curr
	end := curr + length

	if end > regLen || start > end {
		return ""
	}

	b := t.ActionsRegistry[start:end]
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

func (t *RouteTree) insert(url []byte, actionIndex uint32, methods uint16, tags uint16) {
	if len(url) == 0 {
		return
	}

	firstChar := url[0]
	edge := &t.EdgePool[firstChar]
	if edge.Node == nil {
		edge.Fragment = &[]byte{firstChar}
		edge.Node = &RouteNode{
			Edges: &[]Edge{},
		}
	}

	currNode := edge.Node
	for i := 1; i < len(url); i++ {
		char := url[i]
		var nextEdge *Edge
		for j := range *currNode.Edges {
			if (*(*currNode.Edges)[j].Fragment)[0] == char {
				nextEdge = &(*currNode.Edges)[j]
				break
			}
		}

		if nextEdge == nil {
			(*currNode.Edges) = append((*currNode.Edges), Edge{
				Node: &RouteNode{
					Edges: &[]Edge{},
				},
				Fragment: &[]byte{char},
			})
			nextEdge = &(*currNode.Edges)[len(*currNode.Edges)-1]
		}
		currNode = nextEdge.Node
	}

	currNode.ActionIndex = actionIndex
	currNode.Methods = methods
	currNode.Tags = tags
}

// compress merges single-child nodes to form a radix tree.
func (t *RouteTree) compress() int {
	totalLen := 0
	for i := range 256 {
		if t.EdgePool[i].Node != nil {
			totalLen += t.compressNode(t.EdgePool[i].Node)
		}
	}
	return totalLen
}

func (t *RouteTree) compressEdge(e *Edge) (*Edge, int, bool) {
	if e.Node == nil {
		return nil, 0, false
	}

	isWildcard := (*e.Fragment)[0] == Wildcard || (*e.Fragment)[0] == WildcardGreedy
	switch len(*e.Node.Edges) {
	case 0:
		if isWildcard {
			return e, 0, false
		}
		return e, 1, true
	case 1:
		child, l, ok := t.compressEdge(&(*e.Node.Edges)[0])
		if isWildcard {
			return e, l, false
		}
		if ok && e.Node.Methods == 0 {
			(*e.Fragment) = append(*e.Fragment, (*child.Fragment)...)
			e.Node = child.Node
		}
		return e, (l + 1), true
	default:
		l := t.compressNode(e.Node)
		return e, (l + 1), true
	}
}

func (t *RouteTree) compressNode(n *RouteNode) int {
	total := 0
	for i := range *n.Edges {
		_, l, _ := t.compressEdge(&(*n.Edges)[i])
		total += l
	}
	// Ensure wildcard is always the last edge for searching priority
	slices.SortFunc(*n.Edges, func(a, b Edge) int {
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
		return typeOf(*a.Fragment) - typeOf(*b.Fragment)
	})
	return total
}

func (t *RouteTree) finalize(estimatedLen int) {
	t.NodePool = make([]RouteNode, 1) // 0 index is reserved/null
	t.FragmentPool = make([]byte, 2, 2+estimatedLen)
	t.FragmentPool[0] = WildcardGreedy
	t.FragmentPool[1] = Wildcard

	var wildcardEdge *Edge
	for i := range 256 {
		if t.EdgePool[i].Node != nil {
			isWildcard := (*t.EdgePool[i].Fragment)[0] == Wildcard || (*t.EdgePool[i].Fragment)[0] == WildcardGreedy

			var wildcardIdx int32
			if isWildcard {
				wildcardIdx++
			}

			edge := t.flattenNode(&t.EdgePool[i], wildcardEdge, wildcardIdx)
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

func (t *RouteTree) flattenNode(e *Edge, pw *Edge, pwIdx int32) Edge {
	firstChar := (*e.Fragment)[0]
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
			edge.GraftRollback += int32(len(*e.Fragment))
		}
		wildcardEdge = edge
	}

	edges := make([]Edge, 0, len(*e.Node.Edges)+1)
	for i := len(*e.Node.Edges) - 1; i >= 0; i-- {
		edge := t.flattenNode(&(*e.Node.Edges)[i], wildcardEdge, wildcardIndex)

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
		offset = uint32((*e.Fragment)[0]) - 1
		end = offset + 1
	} else {
		offset = uint32(len(t.FragmentPool))
		t.FragmentPool = append(t.FragmentPool, (*e.Fragment)...)
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
		res |= matchMethodToken(strings.TrimSpace(m))
	}
	return res
}

func matchMethodToken(m string) uint16 {
	switch m {
	case "g", "get":
		return MethodGet
	case "p", "po", "post":
		return MethodPost
	case "pu", "put":
		return MethodPut
	case "d", "del", "delete":
		return MethodDelete
	case "head":
		return MethodHead
	case "connect":
		return MethodConnect
	case "options":
		return MethodOptions
	case "trace":
		return MethodTrace
	case "patch":
		return MethodPatch
	case "*", "any":
		return MethodAny
	default:
		if m != "" {
			log.Printf("[rtree] Warning: unknown HTTP method token ignored: %s", m)
		}
		return 0
	}
}

// ReverseHost reverses the host part of the URL for better indexing (e.g., com.google.www)
func ReverseHost(url []byte) []byte {
	slashIdx := slices.Index(url, '/')
	if slashIdx == -1 {
		slashIdx = len(url)
	}
	if slashIdx <= 1 {
		return url
	}

	hostPart := string(url[:slashIdx])
	segments := strings.Split(hostPart, ".")
	slices.Reverse(segments)

	newHost := strings.Join(segments, ".")
	return append([]byte(newHost), url[slashIdx:]...)
}

func (t *RouteTree) PrintTree() {
	fmt.Println(".")
	var validRoots []int
	for i := range 256 {
		if t.EdgePool[i].TargetID != 0 {
			validRoots = append(validRoots, i)
		}
	}

	for i, charIdx := range validRoots {
		isLast := i == len(validRoots)-1
		t.printEdge(&t.EdgePool[charIdx], "", isLast)
	}
}

func (t *RouteTree) printEdge(e *Edge, prefix string, isLast bool) {
	node := &t.NodePool[e.TargetID]
	fragmentBytes := bytes.ReplaceAll(t.FragmentPool[e.Offset:e.End], []byte{Wildcard}, []byte{'*'})
	fragmentBytes = bytes.ReplaceAll(fragmentBytes, []byte{WildcardGreedy}, []byte{'*', '*'})
	fragment := string(fragmentBytes)

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	info := ""
	if node.Methods != 0 {
		if node.Methods == MethodAny {
			info = fmt.Sprintf("\t--[%d]-ANY", e.TargetID)
		} else {
			methods := make([]string, 0)
			for i, method := range HTTPMethodMap {
				if node.Methods&method != 0 {
					methods = append(methods, i)
				}
			}
			info = fmt.Sprintf("\t--[%d]-%s", e.TargetID, strings.Join(methods, ","))
		}
	}
	graft := ""
	if e.GraftRollback != 0 {
		graft = fmt.Sprintf("\tg:%d", e.GraftRollback)
	}
	fmt.Printf("%s%s%s%s%s\n", prefix, connector, fragment, info, graft)

	newPrefix := prefix
	if isLast {
		newPrefix += "    "
	} else {
		newPrefix += "│   "
	}

	for i := range node.EdgeCount {
		childIsLast := i == node.EdgeCount-1
		t.printEdge(&t.EdgePool[node.EdgeOffset+uint16(i)], newPrefix, childIsLast)
	}
}
