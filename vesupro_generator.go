package main

import (
    "os"
    "strings"
    "fmt"
    "log"
    "go/parser"
    "go/token"
    "path/filepath"
    . "github.com/d-s-d/simprogtext"
    apidistiller "github.com/d-s-d/vesupro/apidistiller"
)

const usageStr = `$ vesupro-gotranslator <packageName> [<outputFileName>]

generates vesupro parser functions for all .go source files in the current
directory.`

const defaultOutputFname = "vesupro_api.go"

type parameter apidistiller.Parameter
type method apidistiller.Method
type api apidistiller.API

func (p *parameter) OutputFetchToken(f SimProgFile,
v DynSSAVar, tokzrVar Var) {
    if p.IsStruct {
        f.AddLine(`%s := %s.CurrentToken()`, v.NextType("[]byte"),
            tokzrVar.VarName())
    } else {
        f.AddLine(`%s := string(%s.CurrentToken())`, v.NextType("string"),
            tokzrVar.VarName())
    }
}

func (p *parameter) OutputConvString(f SimProgFile, v DynSSAVar) {
    if v.GetType() != "string" {
        f.AddLine(`%s := string(%s.CurrentToken())`, v.NextType("string"))
    }
}

func (p *parameter) OutputParseFunction(f SimProgFile,
v DynSSAVar, errVar Var) error {
    if p.IsStruct {
        currentName := v.VarName()
        f.AddLine("%s := &%s{}", v.Next(), p.TypeName)
        f.AddLine("%s = %s.UnmarshalJSON(t.CurrentToken())", errVar.VarName(),
        currentName)
    } else {
        if v.GetType() != "string" {
            p.OutputConvString(f, v)
        }
        switch p.TypeName {
        case "uint":
            f.AddLine("%[2]s, %[3]s := strconv.ParseUInt(%[1]s, 10, 0)",
            v.VarName(), v.Next(), errVar.VarName())

        case "uint8", "uint16", "uint32", "uint64":
            f.AddLine("%[2]s, %[4]s := strconv.ParseUInt(%[1]s, 10, %[3]s)",
            v.VarName(), v.Next(), p.TypeName[4:], errVar.VarName())

        case "int", "rune":
            f.AddLine("%[2]s, %[3]s := strconv.ParseInt(%[1]s, 10, 0)",
            v.VarName(), v.Next(), errVar.VarName())

        case "int8", "int16", "int32", "int64":
            f.AddLine("%[2]s, %[4]s := strconv.ParseInt(%[1]s, 10, %[3]s)",
            v.VarName(), v.Next(), p.TypeName[3:], errVar.VarName())

        case "float32", "float64":
            f.AddLine("%[2]s, %[4]s := strconv.ParseFloat(%[1]s, %[3]s)",
            v.VarName(), v.Next(), p.TypeName[5:], errVar.VarName())

        case "complex64", "complex128":
            f.AddLine("%[2]s, %[4]s := strconv.ParseFloat(%[1]s, %[3]s)",
            v.VarName(), v.Next(), p.TypeName[7:], errVar.VarName())

        case "byte":
            f.AddLine("%[2]s, %[3]s := strconv.ParseUInt(%[1]s, 10, 8)",
            v.VarName(), v.Next(), errVar.VarName())

        case "bool":
            f.AddLine("%[2]s, %[3]s := strconv.ParseBool(%[1]s)",
            v.VarName(), v.Next(), errVar.VarName())

        case "string":
            // no further processing necessary
        default:
            return fmt.Errorf("Unsupport type: %s", p.TypeName)
        }
    }
    return nil
}

func (p *parameter) OutputTokenConditionVar(
    f SimProgFile, tokVar Var, errVar Var) error {
    tokens, found := apidistiller.BasicTypes[p.TypeName]
    if !found {
        return fmt.Errorf("Type not found: %s.", p.TypeName)
    }
    outputTokenCondition(f, tokens, tokVar, errVar)
    return nil
}

func outputTokenCondition(f SimProgFile, tokens []string,
        tokVar, errVar Var) {

    if len(tokens) < 1 { return }
    // postcond: len(tokens) > 0
    conditions := make([]string, len(tokens))
    for i, token := range tokens {
        conditions[i] = tokVar.VarName() + " != " + token
    }
    errFmt := "%d" + strings.Repeat(", or %d", len(tokens)-1)
    errArg := strings.Join(tokens, ", ")
    condExpr := strings.Join(conditions, " && ")

    f.AddLineIndent("if %s {", condExpr)
    f.AddLineIndent(`%s = fmt.Errorf(`, errVar.VarName())
    f.AddLine(`"Expected token id %s, but got %%d.", %s, %s)`,
        errFmt, errArg, tokVar.VarName())
    f.Unindent()
    f.AddLineUnindent("}")
}

func outputScanToken(f SimProgFile,
    tokVar Var, tokzrVar Var) {
    f.AddLine("%s = vesupro.Scan(%s, true)", tokVar.VarName(),
        tokzrVar.VarName())
}

func outputCheckErrorReturn(f SimProgFile, errVar Var,
nilVals []string) {
    retArgs := make([]string, len(nilVals)+1)
    copy(retArgs, nilVals)
    retArgs[len(nilVals)] = errVar.VarName()

    f.AddLineIndent("if %s != nil {", errVar.VarName())
    f.AddLine("return %s", strings.Join(retArgs, ", "))
    f.AddLineUnindent("}")
}

func (p *parameter) outputParseParameter(f SimProgFile,
paramVar DynSSAVar, tokVar, tokzrVar, errVar Var) error {
    var err error
    nilRetVals := []string{"nil"}

    outputScanToken(f, tokVar, tokzrVar)
    p.OutputTokenConditionVar(f, tokVar, errVar)
    outputCheckErrorReturn(f, errVar, nilRetVals)

    p.OutputFetchToken(f, paramVar, tokzrVar)
    err = p.OutputParseFunction(f, paramVar, errVar)
    if err != nil { return err }
    outputCheckErrorReturn(f, errVar, nilRetVals)

    return nil
}

func (m *method) OutputParseParameters(f SimProgFile,
paramVars []DynSSAVar, tokVar, tokzrVar, errVar Var) error {

    nilRetVals := []string{"nil"}

    if len(m.Params) == 0 {
        return nil
    }

    (*parameter)(m.Params[0]).outputParseParameter(f, paramVars[0],
        tokVar, tokzrVar, errVar)

    for i, param := range m.Params[1:] {
        p := (*parameter)(param)
        outputScanToken(f, tokVar, tokzrVar)
        outputTokenCondition(f, []string{"vesupro.COMMA"}, tokVar, errVar)
        outputCheckErrorReturn(f, errVar, nilRetVals)

        err := p.outputParseParameter(f, paramVars[i+1], tokVar, tokzrVar,
            errVar)
        if err != nil { return err }
    }
    return nil
}

func (m *method) outputCall(f SimProgFile,
tokVar, tokzrVar, errVar, rcvVar Var) error {
    paramVars := make([]DynSSAVar, len(m.Params))
    finalParams := make([]string, len(m.Params))

    for i, param := range m.Params {
        paramVars[i] = NewDynSSAVar(fmt.Sprintf(
            "param_%d", param.Position), "")
    }

    err := m.OutputParseParameters(f, paramVars, tokVar, tokzrVar, errVar)
    if err != nil {
        return err
    }

    for i, param := range m.Params {
        if param.IsStruct {
            finalParams[i] = paramVars[i].VarName()
        } else {
            finalParams[i] = fmt.Sprintf("%s(%s)", param.TypeName,
                paramVars[i].VarName())
        }
    }

    f.AddLine("return %s.%s(%s)",
    rcvVar.VarName(), m.Name, strings.Join(finalParams, ", "))

    return nil
}

func (a *api) outputDispatchers(f SimProgFile) {
    mNameParam := NewSimpleVar("methodName")
    tokzrVar := NewSimpleVar("t")
    tokVar := NewSimpleVar("tok")
    errVar := NewSimpleVar("err")
    rcvVar := NewSimpleVar("r")

    for rcvTypeName, methods := range a.Methods {

        f.AddLineIndent(
            "func (%s *%s) Dispatch(%s string, %s vesupro.Tokenizer) " +
            "(vesupro.VesuproObject, error) {",
            rcvVar.VarName(), rcvTypeName, mNameParam.VarName(),
            tokzrVar.VarName())
        f.AddLine("var %s vesupro.Token", tokVar.VarName())
        f.AddLine("var %s error", errVar.VarName())

        f.AddLine("switch %s {", mNameParam.VarName())

        for _, _m := range methods {
            m := (*method)(_m)
            f.AddLineIndent("case %q:", m.Name)
            m.outputCall(f, tokVar, tokzrVar, errVar, rcvVar)
            f.Unindent()
        }

        f.AddLineIndent("default:")
        f.AddLine(`return nil,fmt.Errorf("Unknown function %%s.", methodName)`)
        f.AddLineUnindent("}") // switch
        f.AddLineUnindent("}") // DispatchCall function
    }
}

func (a *api) outputPrelude(f SimProgFile) {
    f.AddLine("package " + a.PackageName)
    f.AddLineIndent("import (")
    f.AddLine(`"fmt"`)
    f.AddLine(`"strconv"`)
    f.AddLine(`"github.com/d-s-d/vesupro"`)
    f.AddLineUnindent(")")
}

// # MAIN #
func main() {
    var (
        err error
    )

    nArgs := len(os.Args)

    if nArgs < 2 {
        fmt.Println("usage: ");
        return
    }

    packageName := os.Args[1]

    outputFilename := defaultOutputFname
    if nArgs > 2 {
        outputFilename = os.Args[2]
    }

    fOut, err := os.Create(outputFilename)
    if err != nil {
        log.Fatal(err);
    }

    outText := NewBufferedSimProgFile(fOut)

    a := apidistiller.NewAPI(packageName)

    // walk through all .go files
    goFiles, err := filepath.Glob("./*.go")
    if err != nil { log.Fatal(err) }

    for _, goFile := range goFiles {
        if goFile == outputFilename { continue }
        fset := token.NewFileSet()

        f, err := parser.ParseFile(fset, goFile, nil, parser.ParseComments)
        if err != nil {
            log.Print(err)
            continue
        }
        if f.Name.Name != packageName {
            log.Printf("Ignoring %q (wrong package name %q).", goFile,
                f.Name.Name)
            continue
        }

        a.DistillFromAstFile(f)
    }

    _api := (*api)(a)
    _api.outputPrelude(outText)
    _api.outputDispatchers(outText)
    outText.WriteToFile()
}
