package servicetracer

import (
	"bytes"
	"fmt"
	"log"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-graphviz/cgraph"
	"golang.org/x/xerrors"
)

type Renderer struct {
	cfg *Config
}

func NewRenderer(cfg *Config) *Renderer {
	return &Renderer{cfg: cfg}
}

func (r *Renderer) Render(methodMap map[string][]*Method) error {
	g := graphviz.New()
	graph, err := g.Graph()
	if err != nil {
		return xerrors.Errorf("failed to create graphviz graph: %w", err)
	}
	defer func() {
		if err := graph.Close(); err != nil {
			log.Fatalf("failed to close graphviz graph %s", err)
		}
		g.Close()
	}()
	graph.SetRankDir(cgraph.LRRank)
	graph.SetNewRank(true)
	for _, service := range r.cfg.Services {
		subgraph := graph.SubGraph(fmt.Sprintf("cluster%s", service.Name), 1)
		mtds, err := service.Methods()
		if err != nil {
			return xerrors.Errorf("failed to parse proto file: %w", err)
		}
		for _, mtd := range mtds {
			fromName := fmt.Sprintf("%s.%s", service.Name, mtd.Name)
			from, err := subgraph.CreateNode(fmt.Sprintf("%s.%s.%s", service.Name, service.Name, mtd.Name))
			if err != nil {
				return xerrors.Errorf("failed to create node: %w", err)
			}
			from.SetLabel(fromName)
			from.SetShape(cgraph.BoxShape)
			calledMethods, exists := methodMap[mtd.MangledName()]
			if !exists {
				continue
			}
			edgeMap := map[string]struct{}{}
			if err := r.render(subgraph, service.Name, edgeMap, from, mtd, calledMethods, methodMap); err != nil {
				return xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	var b bytes.Buffer
	g.Render(graph, graphviz.XDOT, &b)
	g.RenderFilename(graph, graphviz.SVG, "result.svg")
	return nil
}

func (r *Renderer) render(graph *cgraph.Graph, serviceName string, edgeMap map[string]struct{}, fromNode *cgraph.Node, from *Method, methods []*Method, methodMap map[string][]*Method) error {
	fromName := fmt.Sprintf("%s.%s", from.Service, from.Name)
	for _, to := range methods {
		if serviceName == to.Service {
			continue
		}
		if fromName == fmt.Sprintf("%s.%s", to.Service, to.Name) {
			continue
		}
		toName := fmt.Sprintf("%s.%s.%s", serviceName, to.Service, to.Name)
		edgeName := fmt.Sprintf("%s.%s", fromName, toName)
		if _, exists := edgeMap[edgeName]; exists {
			continue
		}
		edgeMap[edgeName] = struct{}{}
		toNode, err := graph.CreateNode(toName)
		if err != nil {
			return xerrors.Errorf("failed to create node: %w", err)
		}
		toNode.SetLabel(fmt.Sprintf("%s.%s", to.Service, to.Name))
		toNode.SetShape(cgraph.BoxShape)
		if _, err := graph.CreateEdge(fmt.Sprintf("%s.%s", fromName, toName), fromNode, toNode); err != nil {
			return xerrors.Errorf("failed to create edge: %w", err)
		}
		toMethods, exists := methodMap[to.MangledName()]
		if exists {
			if err := r.render(graph, serviceName, edgeMap, toNode, to, toMethods, methodMap); err != nil {
				return xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	return nil
}
