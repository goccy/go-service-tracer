package servicetracer

import (
	"bytes"
	"fmt"
	"image/color"
	"io/ioutil"
	"log"
	"text/template"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-graphviz/cgraph"
	"golang.org/x/xerrors"
)

type Renderer struct {
	cfg      *Config
	colorIdx int
}

func NewRenderer(cfg *Config) *Renderer {
	return &Renderer{
		cfg:      cfg,
		colorIdx: 0,
	}
}

var (
	colors = []color.Color{
		color.RGBA{0xc7, 0x15, 0x85, 0xff}, // MediumVioletRed
		color.RGBA{0xdc, 0x14, 0x3c, 0xff}, // Crimson
		color.RGBA{0xff, 0x45, 0x00, 0xff}, // OrangeRed
		color.RGBA{0x00, 0x64, 0x00, 0xff}, // DarkGreen
		color.RGBA{0x00, 0xce, 0xd1, 0xff}, // DarkTurquoise
		color.RGBA{0x41, 0x69, 0xe1, 0xff}, // RoyalBlue
		color.RGBA{0x94, 0x00, 0xd3, 0xff}, // DarkViolet
	}
)

func byteToHex(b byte) string {
	hex := fmt.Sprintf("%x", b)
	if b < 10 {
		return "0" + hex
	}
	return hex
}

func (r *Renderer) generateColor() string {
	paletteSize := len(colors)
	if r.colorIdx < paletteSize {
		red, green, blue, _ := colors[r.colorIdx].RGBA()
		color := fmt.Sprintf("#%s%s%s", byteToHex(byte(red)), byteToHex(byte(green)), byteToHex(byte(blue)))
		r.colorIdx++
		return color
	}
	r.colorIdx = 0
	return r.generateColor()
}

func (r *Renderer) renderAllGraph(methodMap MethodMap) (string, error) {
	g := graphviz.New()
	graph, err := g.Graph()
	if err != nil {
		return "", xerrors.Errorf("failed to create graphviz graph: %w", err)
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
			return "", xerrors.Errorf("failed to parse proto file: %w", err)
		}
		for _, mtd := range mtds {
			fromName := fmt.Sprintf("%s.%s", service.Name, mtd.Name)
			from, err := subgraph.CreateNode(fmt.Sprintf("%s.%s.%s", service.Name, service.Name, mtd.Name))
			if err != nil {
				return "", xerrors.Errorf("failed to create node: %w", err)
			}
			from.SetLabel(fromName)
			from.SetShape(cgraph.BoxShape)
			analyzedMethod, exists := methodMap[mtd.MangledName()]
			if exists {
				from.SetURL(analyzedMethod.SourceURL)
			} else {
				from.SetColor("#c9c9c9")
				continue
			}
			if len(analyzedMethod.Methods) == 0 {
				continue
			}
			edgeMap := map[string]struct{}{}
			edgeColor := r.generateColor()
			if err := r.render(subgraph, service.Name, edgeColor, edgeMap, from, mtd, analyzedMethod, methodMap); err != nil {
				return "", xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	var b bytes.Buffer
	g.Render(graph, graphviz.SVG, &b)
	return b.String(), nil
}

func (r *Renderer) renderServiceGraph(service *Service, methodMap MethodMap) (string, error) {
	g := graphviz.New()
	graph, err := g.Graph()
	if err != nil {
		return "", xerrors.Errorf("failed to create graphviz graph: %w", err)
	}
	defer func() {
		if err := graph.Close(); err != nil {
			log.Fatalf("failed to close graphviz graph %s", err)
		}
		g.Close()
	}()
	graph.SetRankDir(cgraph.LRRank)
	graph.SetNewRank(true)
	mtds, err := service.Methods()
	if err != nil {
		return "", xerrors.Errorf("failed to parse proto file: %w", err)
	}
	for _, mtd := range mtds {
		fromName := fmt.Sprintf("%s.%s", service.Name, mtd.Name)
		from, err := graph.CreateNode(fmt.Sprintf("%s.%s.%s", service.Name, service.Name, mtd.Name))
		if err != nil {
			return "", xerrors.Errorf("failed to create node: %w", err)
		}
		from.SetLabel(fromName)
		from.SetShape(cgraph.BoxShape)
		analyzedMethod, exists := methodMap[mtd.MangledName()]
		if exists {
			from.SetURL(analyzedMethod.SourceURL)
		} else {
			from.SetColor("#c9c9c9")
			continue
		}
		if len(analyzedMethod.Methods) == 0 {
			continue
		}
		edgeMap := map[string]struct{}{}
		edgeColor := r.generateColor()
		if err := r.render(graph, service.Name, edgeColor, edgeMap, from, mtd, analyzedMethod, methodMap); err != nil {
			return "", xerrors.Errorf("failed to render graph: %w", err)
		}
	}
	var b bytes.Buffer
	g.Render(graph, graphviz.SVG, &b)
	return b.String(), nil
}

type serviceGraph struct {
	Name  string
	Graph string
}

type renderParam struct {
	All      string
	Services []*serviceGraph
}

func (r *Renderer) Render(methodMap MethodMap) error {
	tmpl, err := template.New("graph.tmpl").Parse(outputHTML)
	if err != nil {
		return xerrors.Errorf("failed to parse template HTML: %w", err)
	}
	all, err := r.renderAllGraph(methodMap)
	if err != nil {
		return xerrors.Errorf("failed to render all graph: %w", err)
	}
	graphs := []*serviceGraph{}
	for _, service := range r.cfg.Services {
		graph, err := r.renderServiceGraph(service, methodMap)
		if err != nil {
			return xerrors.Errorf("failed to render service graph: %w", err)
		}
		graphs = append(graphs, &serviceGraph{
			Name:  service.Name,
			Graph: graph,
		})
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, renderParam{
		All:      all,
		Services: graphs,
	}); err != nil {
		return xerrors.Errorf("failed to execute template: %w", err)
	}
	if err := ioutil.WriteFile(fmt.Sprintf("%s.html", r.cfg.Output), b.Bytes(), 0644); err != nil {
		return xerrors.Errorf("failed to write %s.html: %w", r.cfg.Output, err)
	}
	return nil
}

func (r *Renderer) render(
	graph *cgraph.Graph,
	serviceName string,
	edgeColor string,
	edgeMap map[string]struct{},
	fromNode *cgraph.Node,
	from *Method,
	analyzedMethod *AnalyzedMethod,
	methodMap MethodMap) error {

	fromName := fmt.Sprintf("%s.%s", from.Service, from.Name)
	for _, to := range analyzedMethod.Methods {
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
		edge, err := graph.CreateEdge(fmt.Sprintf("%s.%s", fromName, toName), fromNode, toNode)
		if err != nil {
			return xerrors.Errorf("failed to create edge: %w", err)
		}
		edge.SetColor(edgeColor)
		toMethods, exists := methodMap[to.MangledName()]
		if exists {
			toNode.SetURL(toMethods.SourceURL)
			if err := r.render(graph, serviceName, edgeColor, edgeMap, toNode, to, toMethods, methodMap); err != nil {
				return xerrors.Errorf("failed to render graph: %w", err)
			}
		}
	}
	return nil
}

const outputHTML = `
<html>
  <link rel="stylesheet" href="https://maxcdn.bootstrapcdn.com/bootstrap/4.0.0-beta.3/css/bootstrap.min.css" integrity="sha384-Zug+QiDoJOrZ5t4lssLdxGhVrurbmBWopoEl+M6BdEfwnCJZtKxi1KgxUyJq13dy" crossorigin="anonymous">
  <style type="text/css">
#list {
    height:100vh;
    overflow-y:scroll;
}

#ref {
    height: 100vh;
    overflow-y:scroll;
}
  </style>
  <script type="text/javascript">
    function selectService(serviceName) {
        let serviceGraph = document.getElementById(serviceName).innerHTML;
        document.getElementById("ref").innerHTML = serviceGraph;
        document.getElementById("title").innerHTML = serviceName + " method dependencies";
    };
  </script>
  <body>
    <div class="row">
      <div id="list" class="col-3">
        <ul class="list-group">
          <div id="all" style="display:none">{{ .All }}</div>
          <li class="list-group-item list-group-item-action" onClick="selectService('all')">ALL</li>
          {{- range .Services }}
          <div id="{{ .Name }}" style="display:none">{{ .Graph }}</div>
          <li class="list-group-item list-group-item-action" onClick="selectService('{{ .Name }}')">{{ .Name }}</li>
          {{- end }}
        </ul>
      </div>
      <div class="col-9">
        <div>
          <h3 id="title"></h3>
          <div id="ref">NONE</div>
        </div>
      </div>
    </div>
  </body>
  <script type="text/javascript">selectService("all");</script>
</html>
`
