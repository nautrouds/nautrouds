package proxy

import (
	"nautrouds/internal/core/builtins"
	"nautrouds/internal/core/builtins/builtinsmware"
	"nautrouds/internal/core/builtins/virtualservices"
	"nautrouds/internal/rtree"
	"net/http"
	"strings"
)

type Generation struct {
	Tree      rtree.RouteTree
	builtins  map[string]builtinsmware.HandlerFunc // opLen==0
	virtuals  map[string]http.HandlerFunc
	externals map[string]ExternalMW
}

type ExternalMW struct {
	FuncName       string
	Path           string
	AllowedHeaders []string
}

func (g *Generation) InitCaches() {
	g.builtins = make(map[string]builtinsmware.HandlerFunc)
	g.virtuals = make(map[string]http.HandlerFunc)
	g.externals = make(map[string]ExternalMW)

	tree := &g.Tree
	for i := range tree.NodePool {
		node := &tree.NodePool[i]
		if node.Methods == 0 {
			continue
		}

		g.prewarmService(tree, tree.ActionMetadata[node.ActionIndex])

		mwCount := tree.ActionMetadata[node.ActionIndex+1]
		baseOffset := node.ActionIndex + 2
		for j := uint32(0); j < mwCount; j++ {
			g.prewarmMiddleware(tree, tree.ActionMetadata[baseOffset+j])
		}
	}
}

func (g *Generation) prewarmService(tree *rtree.RouteTree, metaIndex uint32) {
	if tree.ActionMetadata[metaIndex+1] > 0 {
		return
	}
	name := tree.GetActionName(tree.ActionMetadata[metaIndex])
	if !strings.HasPrefix(name, "$") {
		return
	}
	if _, ok := g.virtuals[name]; ok {
		return
	}
	if handler := buildVirtualHandler(name); handler != nil {
		g.virtuals[name] = handler
	}
}

func (g *Generation) prewarmMiddleware(tree *rtree.RouteTree, metaIndex uint32) {
	if tree.ActionMetadata[metaIndex+1] > 0 {
		return
	}
	name := tree.GetActionName(tree.ActionMetadata[metaIndex])

	if strings.HasPrefix(name, "$") {
		if _, ok := g.builtins[name]; ok {
			return
		}
		if handler := buildBuiltinHandler(name); handler != nil {
			g.builtins[name] = handler
		}
		return
	}

	if _, ok := g.externals[name]; ok {
		return
	}
	g.externals[name] = buildExternalMW(name)
}

func buildVirtualHandler(expr string) http.HandlerFunc {
	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return nil
	}
	factory, ok := virtualservices.Registry[funcName]
	if !ok || factory == nil { // nil == $services/$ping, runtime-only
		return nil
	}
	handler, err := factory(args...)
	if err != nil {
		return nil
	}
	return handler
}

func buildBuiltinHandler(expr string) builtinsmware.HandlerFunc {
	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return nil
	}
	factory, ok := builtinsmware.Registry[funcName]
	if !ok {
		return nil
	}
	handler, err := factory(args...)
	if err != nil {
		return nil
	}
	return handler
}

func buildExternalMW(expr string) ExternalMW {
	funcName, args, err := builtins.ParseDirective(expr)
	if err != nil {
		return ExternalMW{FuncName: expr}
	}
	mw := ExternalMW{FuncName: funcName}
	if len(args) > 0 {
		mw.Path = args[0]
	}
	for _, arg := range args[1:] {
		if header, ok := strings.CutPrefix(arg, "header="); ok {
			mw.AllowedHeaders = append(mw.AllowedHeaders, header)
		}
	}
	return mw
}
