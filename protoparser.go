package servicetracer

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/jhump/protoreflect/desc/protoparse"
	"golang.org/x/xerrors"
)

func ParseProto(serviceName, path string) ([]*Method, error) {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, xerrors.Errorf("failed to readdir %s: %w", path, err)
	}
	protoFiles := []string{}
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".proto" {
			continue
		}
		protoFiles = append(protoFiles, filepath.Join(path, file.Name()))
	}
	var p protoparse.Parser
	results, err := p.ParseFilesButDoNotLink(protoFiles...)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse proto files: %w", err)
	}
	mtds := []*Method{}
	for _, result := range results {
		opt := result.Options
		var generatedPath string
		if opt.GoPackage != nil {
			generatedPath = *opt.GoPackage
		} else {
			for _, o := range opt.UninterpretedOption {
				for _, name := range o.Name {
					if name.NamePart != nil && *name.NamePart == "go_package" {
						v := string(o.StringValue)
						vv := strings.Split(v, ";")
						generatedPath = vv[0]
						break
					}
				}
			}
		}
		for _, service := range result.Service {
			for _, method := range service.Method {
				mtds = append(mtds, &Method{
					Pkg:           *result.Package,
					GeneratedPath: generatedPath,
					Service:       serviceName,
					Name:          *method.Name,
					InputType:     *method.InputType,
					OutputType:    *method.OutputType,
				})
			}
		}
	}
	return mtds, nil
}
