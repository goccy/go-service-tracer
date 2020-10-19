package servicetracer

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/xerrors"
)

type Analyzer struct {
	cfg *Config
}

func NewAnalyzer(cfg *Config) *Analyzer {
	return &Analyzer{cfg: cfg}
}

func (a *Analyzer) Analyze(service *Service) (map[string][]*Method, error) {
	mtdMap, err := service.MethodNameMap()
	if err != nil {
		return nil, xerrors.Errorf("failed to get method map: %w", err)
	}
	paths, err := service.Entries()
	if err != nil {
		return nil, xerrors.Errorf("failed to get entries: %w", err)
	}
	methodCallMap := map[string][]*Method{}
	fmt.Printf("analyzing %s...\n", service.Name)
	for _, path := range paths {
		mainPkgs, err := a.mainPackages(path)
		if len(mainPkgs) == 0 {
			continue
		}
		cg, err := a.createCallGraph(mainPkgs)
		if err != nil {
			return nil, xerrors.Errorf("failed to create callgraph: %w", err)
		}

		var (
			edgeMap          = map[int][]*callgraph.Node{}
			methodToNodesMap = map[*Method][]*callgraph.Node{}
		)
		if err := callgraph.GraphVisitEdges(cg, func(edge *callgraph.Edge) error {
			caller := edge.Caller
			callerID := caller.ID

			edgeMap[callerID] = append(edgeMap[callerID], edge.Callee)

			mtds, exists := mtdMap[caller.Func.Name()]
			if !exists {
				return nil
			}

			sig := caller.Func.Signature
			params := sig.Params()
			results := sig.Results()

			// gRPC methods has two request parameters.
			// First:  context.Context
			// Second: custom request structure.
			if params.Len() < 2 {
				return nil
			}

			// gRPC methods has two response parameters.
			// First: custom response structure.
			// Second: error
			if results.Len() < 2 {
				return nil
			}

			// First argument of gRPC method expects context.Context.
			if params.At(0).Type().String() != "context.Context" {
				return nil
			}

			var filteredMethods []*Method
			argPkgPath := params.At(1).Pkg().Path()
			for _, mtd := range mtds {
				// Ignore generated package.
				if argPkgPath == mtd.GeneratedPath {
					continue
				}
				inType := fmt.Sprintf("*%s.%s", mtd.GeneratedPath, mtd.InputType)
				if params.At(1).Type().String() != inType {
					continue
				}
				outType := fmt.Sprintf("*%s.%s", mtd.GeneratedPath, mtd.OutputType)
				if results.At(0).Type().String() != outType {
					continue
				}
				filteredMethods = append(filteredMethods, mtd)
			}
			if len(filteredMethods) != 1 {
				return nil
			}
			mtd := filteredMethods[0]
			methodToNodesMap[mtd] = append(methodToNodesMap[mtd], caller)
			return nil
		}); err != nil {
			return nil, xerrors.Errorf("failed to walk edges: %w", err)
		}

		for mtd, nodes := range methodToNodesMap {
			funcs := []*ssa.Function{}
			nodeMap := map[int]struct{}{}
			protoGoRepo := mtd.GeneratedPathToRepo()
			for _, node := range nodes {
				for _, f := range a.getGRPCMethods(service, protoGoRepo, node, edgeMap, nodeMap) {
					funcs = append(funcs, f)
				}
			}

			calledMethodNameMap := map[string]struct{}{}
			for _, f := range funcs {
				calledMethod, err := a.ssaFuncToMethod(mtd.Name, f)
				if err != nil {
					return nil, xerrors.Errorf("failed to convert ssa.Function to Method: %w", err)
				}

				// ignore duplicated methods
				calledMethodName := calledMethod.MangledName()
				if _, exists := calledMethodNameMap[calledMethodName]; exists {
					continue
				}
				calledMethodNameMap[calledMethodName] = struct{}{}

				methodName := mtd.MangledName()
				methodCallMap[methodName] = append(methodCallMap[methodName], calledMethod)
			}
		}
	}
	return methodCallMap, nil
}

func (a *Analyzer) createCallGraph(mainPkgs []*ssa.Package) (*callgraph.Graph, error) {
	config := &pointer.Config{
		Mains:          mainPkgs,
		BuildCallGraph: true,
	}
	result, err := pointer.Analyze(config)
	if err != nil {
		return nil, xerrors.Errorf("failed to analyze: %w", err)
	}
	cg := result.CallGraph
	cg.DeleteSyntheticNodes()
	return cg, nil
}

func (a *Analyzer) mainPackages(path string) ([]*ssa.Package, error) {
	pkgs, err := a.loadPackage(path)
	if err != nil {
		return nil, xerrors.Errorf("failed to load package: %w", err)
	}
	return a.filterMainPackages(pkgs), nil
}

func (a *Analyzer) loadPackage(path string) ([]*ssa.Package, error) {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: false,
		Dir:   path,
	}
	pkgs, err := packages.Load(cfg)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil
		}
	}
	prog, allPkgs := ssautil.AllPackages(pkgs, 0)
	prog.Build()
	return allPkgs, nil
}

func (a *Analyzer) filterMainPackages(pkgs []*ssa.Package) []*ssa.Package {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p == nil {
			continue
		}
		if p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	return mains
}

func (a *Analyzer) removePkgPath(typ string) string {
	splitted := strings.Split(typ, ".")
	return splitted[len(splitted)-1]
}

func (a *Analyzer) ssaFuncToMethod(name string, fn *ssa.Function) (*Method, error) {
	sig := fn.Signature

	var (
		inputType  string
		outputType string
	)
	params := sig.Params()
	results := sig.Results()
	if params.Len() > 1 {
		inputType = a.removePkgPath(params.At(1).Type().String())
	}
	if results.Len() > 1 {
		outputType = a.removePkgPath(results.At(0).Type().String())
	}

	generatedPath := fn.Pkg.Pkg.Path()
	serviceName, err := a.cfg.ServiceNameByGeneratedPath(generatedPath)
	if err != nil {
		return nil, xerrors.Errorf("failed to get service name by generated path: %w", err)
	}
	return &Method{
		GeneratedPath: generatedPath,
		Service:       serviceName,
		Name:          name,
		InputType:     inputType,
		OutputType:    outputType,
	}, nil
}

func (a *Analyzer) getGRPCMethods(service *Service, protoGoRepo string, from *callgraph.Node, edgeMap map[int][]*callgraph.Node, nodeMap map[int]struct{}) map[int]*ssa.Function {
	if _, exists := nodeMap[from.ID]; exists {
		return nil
	}
	nodeMap[from.ID] = struct{}{}
	recv := from.Func.Signature.Recv()
	if recv != nil {
		if !strings.Contains(recv.Pkg().Path(), service.Repo) {
			return nil
		}
	} else {
		return nil
	}
	nodes, exists := edgeMap[from.ID]
	if !exists {
		return nil
	}
	funcMap := map[int]*ssa.Function{}
	for _, to := range nodes {
		if a.isGRPCMethod(to, protoGoRepo) {
			funcMap[to.ID] = to.Func
		}
		for k, v := range a.getGRPCMethods(service, protoGoRepo, to, edgeMap, nodeMap) {
			funcMap[k] = v
		}
	}
	return funcMap
}

func (a *Analyzer) isGRPCMethod(node *callgraph.Node, protoGoRepo string) bool {
	path := a.nodeToPkgPath(node)
	if !strings.Contains(path, protoGoRepo) {
		return false
	}
	sig := node.Func.Signature
	params := sig.Params()
	results := sig.Results()
	if params.Len() < 2 {
		return false
	}
	if !strings.Contains(params.At(1).Pkg().Path(), protoGoRepo) {
		return false
	}
	if results.Len() < 2 {
		return false
	}
	if !strings.Contains(results.At(0).Pkg().Path(), protoGoRepo) {
		return false
	}
	return true
}

func (a *Analyzer) nodeToPkgPath(node *callgraph.Node) string {
	if node == nil {
		return ""
	}
	if node.Func == nil {
		return ""
	}
	if node.Func.Pkg == nil {
		return ""
	}
	return node.Func.Pkg.Pkg.Path()
}
