package typesafe

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// publicAPI is the exported surface of this package. It is spelled out here so
// that adding to it, renaming it, or dropping something from it is a deliberate
// edit rather than an accident.
var publicAPI = []string{
	"APIError", "APIError.Error", "APIError.Is", "APIError.RequestID", "APIError.RetryAfter",
	"APIKeyEnv", "Answer", "AnswersOf", "BaseURLEnv",
	"Choice", "Choice.MarshalJSON", "ChoiceAnswer", "ChoiceAnswer.MarshalJSON", "ChoiceAnswer.Type",
	"Client", "Client.CloseIdleConnections", "Client.Into", "Client.SystemOne",
	"ConnectionError", "ConnectionError.Error", "ConnectionError.Is", "ConnectionError.Unwrap",
	"Content", "DefaultBaseURL", "DefaultModel", "DefaultModelEnv", "DefaultRetryPolicy", "DefaultTimeout",
	"ErrAPI", "ErrAuthentication", "ErrBadRequest", "ErrConfig", "ErrConnection", "ErrInternalServer",
	"ErrInvalidQuestions", "ErrInvalidResponse", "ErrNotFound", "ErrPermissionDenied", "ErrRateLimit",
	"ErrTimeout", "ErrUnprocessableEntity",
	"ListModelsResponse", "ListModelsResponse.UnmarshalJSON", "LogLevelEnv",
	"ModelMetadata", "ModelsService", "ModelsService.List", "New", "Noul", "Noul.MarshalJSON",
	"NoulAnswer", "NoulAnswer.MarshalJSON", "NoulAnswer.Type", "NoulCriteria", "Option",
	"Question", "Questions", "RawQuestion", "ResponseMetadata", "ResponseValidationError",
	"ResponseValidationError.Error", "ResponseValidationError.Is", "ResponseValidationError.RequestID",
	"ResponseValidationError.Unwrap", "RetryPolicy",
	"Score", "Score.MarshalJSON", "ScoreAnswer", "ScoreAnswer.MarshalJSON", "ScoreAnswer.Type",
	"SystemOneInto", "SystemOneResponse", "SystemOneResponse.As", "SystemOneResponse.Choices",
	"SystemOneResponse.Nouls", "SystemOneResponse.Scores", "SystemOneResponse.UnmarshalJSON",
	"TimeoutError", "TimeoutError.Error", "TimeoutError.Is", "TimeoutError.Temporary",
	"TimeoutError.Timeout", "TimeoutError.Unwrap",
	"Usage", "Version",
	"WithAPIKey", "WithBaseURL", "WithDotenv", "WithExtraBody", "WithHTTPClient", "WithHeader", "WithHeaders",
	"WithLogger", "WithModel", "WithRetryPolicy", "WithTimeout",
}

func TestPublicAPISurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			found = append(found, exportedNames(decl)...)
		}
	}
	slices.Sort(found)
	want := slices.Clone(publicAPI)
	slices.Sort(want)

	for _, name := range found {
		if !slices.Contains(want, name) {
			t.Errorf("%s is exported but not listed in publicAPI; add it deliberately", name)
		}
	}
	for _, name := range want {
		if !slices.Contains(found, name) {
			t.Errorf("%s is listed in publicAPI but no longer exported", name)
		}
	}
}

// exportedNames lists what a declaration adds to the package's exported surface.
func exportedNames(decl ast.Decl) []string {
	var names []string
	switch node := decl.(type) {
	case *ast.FuncDecl:
		if !node.Name.IsExported() {
			return nil
		}
		if node.Recv == nil {
			return []string{node.Name.Name}
		}
		receiver := receiverName(node.Recv.List[0].Type)
		if !ast.IsExported(receiver) {
			return nil
		}
		return []string{receiver + "." + node.Name.Name}
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				if spec.Name.IsExported() {
					names = append(names, spec.Name.Name)
				}
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					if name.IsExported() {
						names = append(names, name.Name)
					}
				}
			}
		}
	}
	return names
}

// receiverName returns the type a method is defined on, without its pointer or
// type parameters.
func receiverName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.StarExpr:
		return receiverName(node.X)
	case *ast.IndexExpr:
		return receiverName(node.X)
	case *ast.IndexListExpr:
		return receiverName(node.X)
	case *ast.Ident:
		return node.Name
	}
	return ""
}
