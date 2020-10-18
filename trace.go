package servicetracer

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/xerrors"
)

func filterMainPackages(pkgs []*ssa.Package) []*ssa.Package {
	var mains []*ssa.Package
	for _, p := range pkgs {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			mains = append(mains, p)
		}
	}
	return mains
}

func existsMainPackage(path string) bool {
	var fset token.FileSet
	pkgs, err := parser.ParseDir(&fset, path, nil, 0)
	if err != nil {
		return false
	}
	return pkgs["main"] != nil
}

func loadPackage(path string) ([]*ssa.Package, error) {
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

var (
	testPattern = regexp.MustCompile(`_test\.go$`)
)

func containsGoFile(dir string) bool {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return false
	}
	for _, path := range files {
		file := filepath.Base(path)
		if strings.HasPrefix(file, ".") {
			continue
		}
		if strings.HasPrefix(file, "#") {
			continue
		}
		if strings.HasPrefix(file, "~") {
			continue
		}
		if testPattern.MatchString(file) {
			continue
		}
		return true
	}
	return false
}

func nodeToPkgPath(node *callgraph.Node) string {
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

func entries(service *Service) ([]string, error) {
	root := filepath.Join(cacheDir, service.RepoName())
	if service.Entry != "" {
		return []string{filepath.Join(root, service.Entry)}, nil
	}
	paths := []string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return xerrors.Errorf("failed to walk: %w", err)
		}
		if !info.IsDir() {
			return nil
		}
		if info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !containsGoFile(path) {
			return nil
		}
		if !existsMainPackage(path) {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return nil, xerrors.Errorf("failed to walk: %w", err)
	}
	return paths, nil
}

func filterMethods(mtds []*Method, filters ...func(mtd *Method) bool) []*Method {
	out := []*Method{}
	for _, mtd := range mtds {
		passed := true
		for _, filter := range filters {
			if !filter(mtd) {
				passed = false
				break
			}
		}
		if passed {
			out = append(out, mtd)
		}
	}
	return out
}

func Trace(cfg *Config, service *Service) (map[string][]*Method, error) {
	mtds, err := service.Methods()
	if err != nil {
		return nil, xerrors.Errorf("failed to get methods: %w", err)
	}
	mtdMap := map[string][]*Method{}
	for _, mtd := range mtds {
		mtdMap[mtd.Name] = append(mtdMap[mtd.Name], mtd)
	}
	paths, err := entries(service)
	if err != nil {
		return nil, xerrors.Errorf("failed to get entries: %w", err)
	}
	methodCallMap := map[string][]*Method{}
	for _, path := range paths {
		pkgs, err := loadPackage(path)
		if err != nil {
			return nil, xerrors.Errorf("failed to load package: %w", err)
		}
		mainPkgs := filterMainPackages(pkgs)
		if len(mainPkgs) == 0 {
			continue
		}
		fmt.Println("mainPackages = ", mainPkgs)
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
		edgeMap := map[int][]*callgraph.Node{}
		methodToNodesMap := map[*Method][]*callgraph.Node{}
		if err := callgraph.GraphVisitEdges(cg, func(edge *callgraph.Edge) error {
			edgeMap[edge.Caller.ID] = append(edgeMap[edge.Caller.ID], edge.Callee)
			node := edge.Caller
			mtds, exists := mtdMap[node.Func.Name()]
			if !exists {
				return nil
			}
			sig := node.Func.Signature
			params := sig.Params()
			results := sig.Results()
			if params.Len() < 2 {
				return nil
			}
			if results.Len() < 2 {
				return nil
			}
			// first argument of gRPC method is context.Context
			if params.At(0).Type().String() != "context.Context" {
				return nil
			}
			// ignore default generated method
			argPkgPath := params.At(1).Pkg().Path()
			filteredMethods := filterMethods(mtds, func(mtd *Method) bool {
				return argPkgPath != mtd.GeneratedPath
			}, func(mtd *Method) bool {
				inType := fmt.Sprintf("*%s.%s", mtd.GeneratedPath, mtd.InputType)
				return params.At(1).Type().String() == inType
			}, func(mtd *Method) bool {
				outType := fmt.Sprintf("*%s.%s", mtd.GeneratedPath, mtd.OutputType)
				return results.At(0).Type().String() == outType
			})
			if len(filteredMethods) != 1 {
				return nil
			}
			mtd := filteredMethods[0]
			methodToNodesMap[mtd] = append(methodToNodesMap[mtd], edge.Caller)
			return nil
		}); err != nil {
			return nil, xerrors.Errorf("failed to walk edges: %w", err)
		}
		for mtd, nodes := range methodToNodesMap {
			funcs := []*ssa.Function{}
			nodeMap := map[int]struct{}{}
			paths := strings.Split(mtd.GeneratedPath, "/")
			protoGoRepo := strings.Join(paths[:3], "/")
			for _, node := range nodes {
				for _, f := range getGRPCMethods(service, protoGoRepo, node, edgeMap, nodeMap) {
					funcs = append(funcs, f)
				}
			}
			mangledNames := map[string]struct{}{}
			for _, f := range funcs {
				sig := f.Signature
				params := sig.Params()
				var inputType string
				if params.Len() > 1 {
					typ := params.At(1).Type().String()
					splitted := strings.Split(typ, ".")
					inputType = splitted[len(splitted)-1]
				}
				results := sig.Results()
				var outputType string
				if results.Len() > 1 {
					typ := results.At(0).Type().String()
					splitted := strings.Split(typ, ".")
					outputType = splitted[len(splitted)-1]
				}
				generatedPath := f.Pkg.Pkg.Path()
				serviceName, err := cfg.ServiceNameByGeneratedPath(generatedPath)
				if err != nil {
					return nil, xerrors.Errorf("failed to get service name by generated path: %w", err)
				}
				calledMethod := &Method{
					GeneratedPath: generatedPath,
					Service:       serviceName,
					Name:          mtd.Name,
					InputType:     inputType,
					OutputType:    outputType,
				}
				name := calledMethod.MangledName()
				if _, exists := mangledNames[name]; exists {
					continue
				}
				methodCallMap[mtd.MangledName()] = append(methodCallMap[mtd.MangledName()], calledMethod)
				mangledNames[name] = struct{}{}
			}
		}
	}
	return methodCallMap, nil
}

func getGRPCMethods(service *Service, protoGoRepo string, from *callgraph.Node, edgeMap map[int][]*callgraph.Node, nodeMap map[int]struct{}) map[int]*ssa.Function {
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
		path := nodeToPkgPath(to)
		if strings.Contains(path, protoGoRepo) {
			sig := to.Func.Signature
			params := sig.Params()
			results := sig.Results()
			if params.Len() >= 2 && strings.Contains(params.At(1).Pkg().Path(), protoGoRepo) &&
				results.Len() >= 2 && strings.Contains(results.At(0).Pkg().Path(), protoGoRepo) {
				funcMap[to.ID] = to.Func
			}
		}
		for k, v := range getGRPCMethods(service, protoGoRepo, to, edgeMap, nodeMap) {
			funcMap[k] = v
		}
	}
	return funcMap
}
