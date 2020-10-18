package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-graphviz/cgraph"
	servicetracer "github.com/goccy/go-service-tracer"
	"github.com/goccy/go-yaml"
	"golang.org/x/xerrors"
)

const cacheDir = ".service-tracer-cache"

func render(graph *cgraph.Graph, serviceName string, edgeMap map[string]struct{}, fromNode *cgraph.Node, from *servicetracer.Method, methods []*servicetracer.Method, callMap map[string][]*servicetracer.Method) error {
	fromName := fmt.Sprintf("%s.%s", from.Service, from.Name)
	for _, to := range methods {
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
		toMethods, exists := callMap[to.MangledName()]
		if exists {
			if err := render(graph, serviceName, edgeMap, toNode, to, toMethods, callMap); err != nil {
				return xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	return nil
}

func _main() error {
	cfg, err := servicetracer.LoadConfig("trace.yaml")
	if err != nil {
		return xerrors.Errorf("failed to load config: %w", err)
	}
	if err := servicetracer.CloneRepository(cfg); err != nil {
		return xerrors.Errorf("failed to clone repository: %w", err)
	}
	mapsDir := filepath.Join(cacheDir, "maps")
	if err := os.MkdirAll(mapsDir, 0755); err != nil {
		return xerrors.Errorf("failed to create directory %s: %w", mapsDir, err)
	}
	callMap := map[string][]*servicetracer.Method{}
	for _, service := range cfg.Services {
		cachePath := filepath.Join(cacheDir, "maps", fmt.Sprintf("%s.yaml", service.Name))
		if _, err := os.Stat(cachePath); err == nil {
			file, err := ioutil.ReadFile(cachePath)
			if err != nil {
				return xerrors.Errorf("failed to read maps cache: %w", err)
			}
			var cm map[string][]*servicetracer.Method
			if err := yaml.Unmarshal(file, &cm); err != nil {
				return xerrors.Errorf("failed to unmarshal callmap file: %w", err)
			}
			for k, v := range cm {
				callMap[k] = v
			}
			continue
		}
		cm, err := servicetracer.Trace(cfg, service)
		if err != nil {
			return xerrors.Errorf("failed to trace: %w", err)
		}
		b, err := yaml.Marshal(cm)
		if err != nil {
			return xerrors.Errorf("failed to marshal callmap: %w", err)
		}
		if err := ioutil.WriteFile(cachePath, b, 0644); err != nil {
			return xerrors.Errorf("failed to write callmap file: %w", err)
		}
		for k, v := range cm {
			callMap[k] = v
		}
	}
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
	for _, service := range cfg.Services {
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
			calledMethods, exists := callMap[mtd.MangledName()]
			if !exists {
				continue
			}
			edgeMap := map[string]struct{}{}
			if err := render(subgraph, service.Name, edgeMap, from, mtd, calledMethods, callMap); err != nil {
				return xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	var b bytes.Buffer
	g.Render(graph, graphviz.XDOT, &b)
	g.RenderFilename(graph, graphviz.SVG, "result.svg")
	return nil
}

func main() {
	if err := _main(); err != nil {
		log.Fatalf("%+v", err)
	}
}
