package main

import (
    "os"
    "io"
    "strings"
    "fmt"
    "errors"
    "log"
    "regexp"
    "go/ast"
    "go/parser"
    "go/token"
)

const VESUPRO_EXTENSION = "_vesupro.go"
const TOKSTR_FMT = "tokStr_%d"
const PARAM_FMT = "param_%d"
const METHOD_NAME_VAR = "methodName"
const VESUPRO_OBJECT = "VesuproObject"

var VesuproRegexp = regexp.MustCompile("^//\\s*vesupro:\\s*export.*$")

// # SOURCE FILE #
type SourceLine struct {
    Indent int
    Line string
}

type SourceFile struct {
    IndentLevel int
    Lines []*SourceLine
}

func (sf *SourceFile) addSourceLine(line string, args ...interface{}) {
    formatedLine := fmt.Sprintf(line, args...)
    if sf.Lines == nil {
        sf.Lines = make([]*SourceLine, 0)
    }
    sf.Lines = append(sf.Lines, &SourceLine{sf.IndentLevel, formatedLine})
}

func (sf *SourceFile) Unindent() {
    if sf.IndentLevel > 0 {
        sf.IndentLevel -= 1
    }
}

func (sf *SourceFile) addSourceLineIndent(line string, args ...interface{}) {
    sf.addSourceLine(line, args...)
    sf.IndentLevel += 1
}

func (sf *SourceFile) addSourceLineUnindent(line string, args ...interface{}) {
    sf.Unindent()
    sf.addSourceLine(line, args...)
}

func (sf *SourceFile) output(w io.Writer) {
    for _, line := range sf.Lines {
        // indentation
        for i := 0; i < line.Indent; i++ {
            w.Write([]byte("    "))
        }
        // source line
        w.Write([]byte(line.Line))
        // newline
        w.Write([]byte("\n"))
    }
}


// # SOURCE GENERATION #
type Parameter struct {
    Position uint
    IsStruct bool
    Type string
    ParseFunctionTemplate string
    AcceptedTokens []string
}

type MethodCall struct {
    Name string
    Params []*Parameter
}

type MethodCollection struct {
    // maps receiver type to functions
    Methods map[string] []*MethodCall
    PackageName string
}

func NewMethodCollection(name string) *MethodCollection {
    return &MethodCollection{make(map[string][]*MethodCall), name}
}

func (p *Parameter) tokStr() string {
    return fmt.Sprintf(TOKSTR_FMT, p.Position)
}

func (p *Parameter) varName() string {
    return fmt.Sprintf(PARAM_FMT, p.Position)
}

func (p *Parameter) parseFunction() string {
    return fmt.Sprintf(p.ParseFunctionTemplate, p.tokStr())
}

func (p *Parameter) outputParseParameter(sf *SourceFile) error {
    if p.Position > 1 {
        sf.addSourceLine("tok = vesupro.Scan(t, true)")
        sf.addSourceLineIndent("if tok != vesupro.COMMA {")
        sf.addSourceLine(
            `return nil, `+
            `errors.New(fmt.Sprintf("Expected COMMA, got %%d.", tok))`)
        sf.addSourceLineUnindent("}")
    }

    sf.addSourceLine("tok = vesupro.Scan(t, true)")

    if len(p.AcceptedTokens) < 1 {
        return errors.New("no accepted Tokens");
    }
    conditionExpr := "tok != vesupro." + p.AcceptedTokens[0]

    for _, tokenName := range p.AcceptedTokens[1:] {
        conditionExpr += " && tok != vesupro." + tokenName
    }

    if p.IsStruct {
        sf.addSourceLine("%s := &" + p.Type + "{}", p.varName(), p.Position)
        sf.addSourceLine("err = %s.UnmarshalJSON(t.currentToken())",
        p.varName())
    } else {
        sf.addSourceLine("%s := string(t.currentToken())", p.tokStr())
        // check if we get the expected tokens
        sf.addSourceLineIndent("if " + conditionExpr + " {")
        sf.addSourceLine(
            `return nil, errors.New(fmt.Sprintf(` +
            `"Method %%s: Unxpected token %%d for parameter %d`+
            `of type %s.", %s, tok))`, p.Position, p.Type, METHOD_NAME_VAR)
        sf.addSourceLineUnindent("}")

        if p.Type == "bool" {
            sf.addSourceLine("%s := false", p.varName())
            sf.addSourceLine("if tok == vesupro.TRUE { %s = true }", p.varName())
        } else {
            // parse token
            if p.ParseFunctionTemplate != "" {
                sf.addSourceLine(
                    "%s, err := %s", p.varName(), p.parseFunction())
            } else {
                sf.addSourceLine("%s := %s", p.varName(), p.tokStr())
            }
        }
    }
    // check for parse failure
    sf.addSourceLineIndent("if err != nil {")
    sf.addSourceLine(
        `return nil, errors.New(fmt.Sprintf(` +
        `"Method %%s: Failed to parse %%q as type %s.", %s, %s))`,
        p.Type, METHOD_NAME_VAR, p.tokStr())
    sf.addSourceLineUnindent("}")
    return nil
}

func (mc *MethodCall) outputCall(sf *SourceFile) {
    finalParams := make([]string, len(mc.Params))

    for i, param := range mc.Params {
        if param.IsStruct {
            finalParams[i] = param.varName()
        } else {
            finalParams[i] = fmt.Sprintf("%s(%s)", param.Type, param.varName())
        }
    }

    sf.addSourceLine("return r.%s(%s), nil",
    mc.Name, strings.Join(finalParams, ", "));
}

func (mc *MethodCollection) outputDispatchers(sf *SourceFile) {
    for rcvTypeName, mcs := range mc.Methods {
        sf.addSourceLineIndent(
            "func (r *%s) Dispatch(%s string, t vesupro.Tokenizer) " +
            "(vesupro.VesuproObject, error) {", rcvTypeName, METHOD_NAME_VAR)
        sf.addSourceLine("switch %s {", METHOD_NAME_VAR)

        for _, mc := range mcs {
            sf.addSourceLineIndent("case %q:", mc.Name)
            for _, p := range mc.Params {
                err := p.outputParseParameter(sf)
                if err != nil {
                    log.Fatal(err)
                }
            }
            mc.outputCall(sf)
            sf.Unindent()
        }

        sf.addSourceLineIndent("default:")
        sf.addSourceLine(`return nil, errors.New(fmt.Sprintf(` +
        `"Unknown function %%s.", methodName))`)
        sf.Unindent()
        sf.addSourceLine("}") // switch
        sf.addSourceLineUnindent("}") // DispatchCall function
    }
}

func (mc *MethodCollection) parseMethods(f *ast.File) error {
    // iterate through declarations
    for _, decl := range f.Decls {
        fDecl, ok := decl.(*ast.FuncDecl)
        if !ok || fDecl.Recv == nil { continue; }

        var receiverTypeName string

        switch t := fDecl.Recv.List[0].Type.(type) {
        case (*ast.StarExpr):
            if ident, ok := t.X.(*ast.Ident); ok {
                receiverTypeName = ident.Name
            }
        case (*ast.Ident):
            receiverTypeName = t.Name
        }

        _, exists := mc.Methods[receiverTypeName]
        if !exists {
            mc.Methods[receiverTypeName] = make([]*MethodCall, 0)
        }

        // parse methods
        methodCall := &MethodCall{Name: fDecl.Name.Name}
        actualPos := 0
        // parse parameters
        for _, paramField := range fDecl.Type.Params.List {
            parameterTemplate := &Parameter{}

            switch t := paramField.Type.(type) {
            case (*ast.Ident):
                parameterTemplate.Type = t.Name

                switch t.Name {
                case "uint":
                    parameterTemplate.ParseFunctionTemplate =
                    "strconv.ParseUInt(%s, 10, 0)"
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "INT")

                case "uint8", "uint16", "uint32", "uint64":
                    parameterTemplate.ParseFunctionTemplate = fmt.Sprintf(
                        "strconv.ParseUInt(%%s, 10, %s)", t.Name[4:])
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "INT")

                case "int", "rune":
                    parameterTemplate.ParseFunctionTemplate =
                        "strconv.ParseInt(%s, 10, 0)"
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "INT")

                case "int8", "int16", "int32", "int64":
                    parameterTemplate.ParseFunctionTemplate = fmt.Sprintf(
                        "strconv.ParseInt(%%s, 10, %s)", t.Name[3:])
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "INT")

                case "float32", "float64":
                    parameterTemplate.ParseFunctionTemplate = fmt.Sprintf(
                        "strconv.ParseFloat(%%s, 10, %s)", t.Name[5:])
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "FLOAT")

                case "complex64", "complex128":
                    parameterTemplate.ParseFunctionTemplate =
                        "strconv.ParseFloat(%s, 10, 64)"
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "FLOAT")

                case "byte":
                    parameterTemplate.ParseFunctionTemplate =
                    "strconv.ParseUInt(%s, 10, 8)"
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "INT")

                case "bool":
                    parameterTemplate.ParseFunctionTemplate = ""
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "TRUE", "FALSE")

                case "string":
                    parameterTemplate.ParseFunctionTemplate = ""
                    parameterTemplate.AcceptedTokens =
                    append(parameterTemplate.AcceptedTokens, "STRING")

                default:
                    log.Fatal(errors.New(
                        fmt.Sprintf("Unsupport type: %s", t.Name)))
                } // switch ident name
            case (*ast.StarExpr):
                // we just assume that this is a struct
                ident, ok := t.X.(*ast.Ident)
                if !ok {
                    log.Fatal(fmt.Sprintf(
                        "Error when parsing StarExpr: %s", t))
                }
                parameterTemplate.Type = ident.Name
                parameterTemplate.ParseFunctionTemplate = ""
                parameterTemplate.IsStruct = true
            } // switch parameter type

            // iterate over names
            var currentParameter  *Parameter
            for _, _ = range paramField.Names {
                currentParameter = &Parameter{}
                *currentParameter = *parameterTemplate
                currentParameter.Position = uint(actualPos)
                actualPos += 1
                methodCall.Params = append(methodCall.Params,
                currentParameter)
            }
        } // for parameter field

        mc.Methods[receiverTypeName] = append(
            mc.Methods[receiverTypeName], methodCall)
    }
    return nil
}

func (mc *MethodCollection) outputPrelude(sf *SourceFile) {
    sf.addSourceLine("package " + mc.PackageName)
    sf.addSourceLineIndent("import (")
    sf.addSourceLine(`"fmt"`)
    sf.addSourceLine(`"errors"`)
    sf.addSourceLine(`"strconv"`)
    sf.addSourceLine(`"github.com/d-s-d/vesupro"`)
    sf.addSourceLineUnindent(")")
}

// # AUXILIARY FUNCTIONS #
func get_output_filename(filename string) (outputFilename string) {
    ilen := len(filename)
    if len(os.Args) > 2 {
        outputFilename = os.Args[2]
    } else if ilen > 3 && filename[ilen-3:] == ".go" {
        outputFilename = filename[:ilen-3] + VESUPRO_EXTENSION
    } else {
        outputFilename = filename + VESUPRO_EXTENSION
    }
    return
}

// # MAIN #
func main() {
    var (
        err error
    )

    if len(os.Args) < 2 {
        fmt.Println("usage");
        return
    }

    inputFilename := os.Args[1]

    outputFilename := get_output_filename(inputFilename)

    f_out, err := os.Create(outputFilename)
    if err != nil {
        log.Fatal(err);
    }

    fset := token.NewFileSet()

    f, err := parser.ParseFile(fset, inputFilename, nil, parser.ParseComments)
    if err != nil {
        log.Fatal(err)
    }

    sf := &SourceFile{}
    methodCollection := NewMethodCollection(f.Name.Name)
    methodCollection.parseMethods(f)
    methodCollection.outputPrelude(sf)
    methodCollection.outputDispatchers(sf)

    sf.output(f_out)

}
