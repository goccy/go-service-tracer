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
	"github.com/rs/xid"
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

func (r *Renderer) renderServiceGraph(service *Service, methodMap MethodMap) ([]*methodGraph, error) {
	mtds, err := service.Methods()
	if err != nil {
		return nil, xerrors.Errorf("failed to parse proto file: %w", err)
	}
	graphs := []*methodGraph{}
	for _, mtd := range mtds {
		graph, err := r.renderMethodGraph(service, mtd, methodMap)
		if err != nil {
			return nil, xerrors.Errorf("failed to render method graph: %w", err)
		}
		graphs = append(graphs, &methodGraph{
			Name:  mtd.Name,
			Graph: graph,
		})
	}
	return graphs, nil
}

func (r *Renderer) uniqueSubgraph(graph *cgraph.Graph) *cgraph.Graph {
	return graph.SubGraph(fmt.Sprintf("cluster%s", r.generateID()), 1)
}

func (r *Renderer) uniqueNode(graph *cgraph.Graph, name string) (*cgraph.Node, error) {
	node, err := graph.CreateNode(r.generateID())
	if err != nil {
		return nil, xerrors.Errorf("failed to create node: %w", err)
	}
	node.SetLabel(name)
	node.SetShape(cgraph.BoxShape)
	return node, nil
}

func (r *Renderer) uniqueEdge(graph *cgraph.Graph, from *cgraph.Node, to *cgraph.Node) (*cgraph.Edge, error) {
	edge, err := graph.CreateEdge(r.generateID(), from, to)
	if err != nil {
		return nil, xerrors.Errorf("failed to create edge: %w", err)
	}
	edge.SetColor(r.generateColor())
	return edge, nil
}

func (r *Renderer) renderMethodGraph(service *Service, mtd *Method, methodMap MethodMap) (string, error) {
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

	mtdName := fmt.Sprintf("%s.%s", service.Name, mtd.Name)

	from, err := r.uniqueNode(graph, mtdName)
	if err != nil {
		return "", xerrors.Errorf("failed to create unique node: %w", err)
	}
	analyzedMethod, exists := methodMap[mtd.MangledName()]
	if exists {
		from.SetURL(analyzedMethod.SourceURL)
	} else {
		from.SetColor("#c9c9c9")
	}
	if analyzedMethod != nil && len(analyzedMethod.Methods) != 0 {
		edgeMap := map[string]struct{}{}
		if err := r.render(graph, service.Name, edgeMap, from, mtd, analyzedMethod, methodMap); err != nil {
			return "", xerrors.Errorf("failed to render graph: %w", err)
		}
	}
	var b bytes.Buffer
	g.Render(graph, graphviz.SVG, &b)
	return b.String(), nil
}

type serviceGraph struct {
	Name    string
	Methods []*methodGraph
}

type methodGraph struct {
	Name  string
	Graph string
}

type renderParam struct {
	Services []*serviceGraph
}

func (r *Renderer) generateID() string {
	return xid.New().String()
}

func (r *Renderer) Render(methodMap MethodMap) error {
	tmpl, err := template.New("graph.tmpl").Parse(outputHTML)
	if err != nil {
		return xerrors.Errorf("failed to parse template HTML: %w", err)
	}
	graphs := []*serviceGraph{}
	for _, service := range r.cfg.Services {
		mtds, err := r.renderServiceGraph(service, methodMap)
		if err != nil {
			return xerrors.Errorf("failed to render service graph: %w", err)
		}
		graphs = append(graphs, &serviceGraph{
			Name:    service.Name,
			Methods: mtds,
		})
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, renderParam{
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
		toNode, err := r.uniqueNode(graph, fmt.Sprintf("%s.%s", to.Service, to.Name))
		if err != nil {
			return xerrors.Errorf("failed to create unique node: %w", err)
		}
		if _, err := r.uniqueEdge(graph, fromNode, toNode); err != nil {
			return xerrors.Errorf("failed to create edge: %w", err)
		}
		toMethods, exists := methodMap[to.MangledName()]
		if exists {
			toNode.SetURL(toMethods.SourceURL)
			if err := r.render(graph, serviceName, edgeMap, toNode, to, toMethods, methodMap); err != nil {
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

h3 {
    margin: 20px;
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
          {{- range .Services }}
          <div id="{{ .Name }}" style="display:none">
            {{- range .Methods }}
            <h3>{{ .Name }}</h3>
            {{ .Graph }}
            {{- end }}
          </div>
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
  {{- $service := index .Services 0 }}
  <script type="text/javascript">selectService("{{ $service.Name }}");</script>
</html>
`
