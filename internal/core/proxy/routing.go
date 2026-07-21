package proxy

import (
	"nautrouds/internal/rtree"
	"net"
	"net/http"
)

func lookupRoute(s *servingState) bool {
	host, _, err := net.SplitHostPort(s.r.Host)
	if err != nil {
		host = s.r.Host
	}

	lookupPath := host + s.r.URL.Path
	lookupPathBytes := []byte(lookupPath)
	rtree.ReverseHost(lookupPathBytes)
	node, exists := s.tree.Search(lookupPathBytes)
	if !exists {
		s.routePattern = "404"
		http.Error(s.w, "Resource Not Found", http.StatusNotFound)
		return false
	}

	s.node = node
	s.routePattern = lookupPath
	return true
}

func validateMethod(s *servingState) bool {
	methodBit := rtree.HTTPMethodMap[s.r.Method]
	if methodBit == 0 {
		methodBit = rtree.MethodAny
	}
	if s.node.Methods&methodBit == 0 {
		http.Error(s.w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}
