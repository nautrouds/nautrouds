package rtree

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unsafe"
)

type Edge struct {
	TargetID uint32 // Index of the destination node in NodePool
	Offset   uint32 // Start position of the fragment in FragmentPool
	End      uint32 // End position of the fragment in FragmentPool

	// GraftRollback manages cursor adjustments during wildcard backtracking.
	// If > 0: specifies the absolute number of bytes to roll back the URL cursor.
	// If < 0: represents a bitwise NOT index (^GraftRollback) to retrieve the
	// saved cursor state from cursorCheckpoints.
	GraftRollback int32
}

type RouteTree struct {
	FragmentPool    []byte // Contiguous memory for all path fragments
	ActionsRegistry []byte // Registry of middleware & service identifiers
	ActionMetadata  []uint32
	NodePool        []RouteNode // Flattened node storage for cache locality
	EdgePool        []Edge      // Flattened edge storage for cache locality
}

type RouteNode struct {
	ActionIndex uint32
	EdgeOffset  uint16
	EdgeCount   uint16
	Methods     uint16 // Bitmask of allowed HTTP methods; 0 if not a leaf
	Tags        uint16
}

func (t *RouteTree) Search(url []byte) (*RouteNode, bool) {
	urlLen := int32(len(url))
	if urlLen == 0 {
		return nil, false
	}

	firstChar := url[0]
	var currentEdgeIdx uint16
	var cursorCheckpoints [16]int32
	var checkpointIdx int32

	cursor := int32(0)

	switch {
	case t.EdgePool[firstChar].TargetID != 0:
		currentEdgeIdx = uint16(firstChar)
		cursor++
	case t.EdgePool[Wildcard].TargetID != 0:
		currentEdgeIdx = uint16(Wildcard)
		checkpointIdx++
	case t.EdgePool[WildcardGreedy].TargetID != 0:
		currentEdgeIdx = uint16(WildcardGreedy)
		checkpointIdx++
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
					checkpointIdx = ^edge.GraftRollback
				} else {
					childEdge := &t.EdgePool[node.EdgeOffset+node.EdgeCount-1]
					if t.FragmentPool[childEdge.Offset] != WildcardGreedy || childEdge.GraftRollback != edge.GraftRollback {
						checkpointIdx = ^edge.GraftRollback
					}
				}
			}
		case WildcardGreedy:
			if edge.GraftRollback < 0 {
				checkpointIdx = ^edge.GraftRollback
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
					cursor = cursorCheckpoints[^childEdge.GraftRollback]
				default:
					cursorCheckpoints[checkpointIdx] = cursor
					checkpointIdx++
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
