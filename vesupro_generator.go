package main

import (
    "os"
    "strings"
    "fmt"
    "log"
    "go/parser"
    "go/token"
    "path/filepath"
    "github.com/d-s-d/simprogtext"
    apidistiller "github.com/d-s-d/vesupro/apidistiller"
)

const usageStr = `$ vesupro-gotranslator <packageName> [<outputFileName>]

generates vesupro parser functions for all .go source files in the current
directory.`

const defaultOutputFname = "vesupro_api.go"

type parameter apidistiller.Parameter
type method apidistiller.Method
type api apidistiller.API

func (p *parameter) OutputParseArgument(f simprogtext.SimProgFile,
        v simprogtext.DynSSAVar, argVar, errVar simprogtext.Var) error {
    if p.IsStruct {
        currentName := v.VarName()
        f.AddLine("%s := &%s{}", v.Next(), p.TypeName)
        f.AddLine("%s = %s.UnmarshalJSON(%s.TokenContent)", errVar.VarName(),
        currentName, argVar)
    } else {
        switch p.TypeName[:3] {
        case "uin", "int", "byt":
            f.AddLine("%s, %s := %s.ToInt64()", v.NextType("int64"),
                errVar.VarName(), argVar.VarName())
        case "flo", "com":
            f.AddLine("%s, %s := %s.ToFloat64()", v.NextType("float64"),
                errVar.VarName(), argVar.VarName())
        case "boo":
            f.AddLine("%s, %s := %s.ToBool()", v.NextType("bool"),
                errVar.VarName(), argVar.VarName())
        case "str":
            f.AddLine("%s, %s := %s.ToString()", v.NextType("string"),
                argVar.VarName())
        default:
            return fmt.Errorf("Unsupport type: %s", p.TypeName)
        }
    }
    return nil
}

func outputCheckErrorReturn(f simprogtext.SimProgFile,
        errVar simprogtext.Var, nilVals []string) {
    retArgs := make([]string, len(nilVals)+1)
    copy(retArgs, nilVals)
    retArgs[len(nilVals)] = errVar.VarName()

    f.AddLineIndent("if %s != nil {", errVar.VarName())
    f.AddLine("return %s", strings.Join(retArgs, ", "))
    f.AddLineUnindent("}")
}

func (m *method) outputCall(f simprogtext.SimProgFile,
        mCallVar, argsVar, errVar, rcvVar simprogtext.Var,
        nilRetVals []string) error {
    paramVars := make([]simprogtext.DynSSAVar, len(m.Params))
    finalParams := make([]string, len(m.Params))

    f.AddLineIndent("if len(%s.Arguments) != %d {", mCallVar.VarName(),
        len(m.Params))
    f.AddLine(
        `fmt.Errof("Method \"%s\": Wrong number of arguments: %%d (want: %d)",`+
        `len(%s))`, m.Name, len(m.Params), argsVar.VarName())

    f.AddLineUnindent("}")

    var err error
    for i, param := range m.Params {
        paramVars[i] = simprogtext.NewDynSSAVar(fmt.Sprintf(
            "param_%d", param.Position), "")
        // parse Argument
        err = (*parameter)(param).OutputParseArgument(f, paramVars[i],
            simprogtext.NewSimpleVar(fmt.Sprintf("%s[%d]", argsVar.VarName(),
                i)), errVar)

        // go sucks
        if err != nil { return err }

        // check error
        outputCheckErrorReturn(f, errVar, nilRetVals)
    }

    for i, param := range m.Params {
        if param.IsStruct {
            finalParams[i] = paramVars[i].VarName()
        } else {
            finalParams[i] = fmt.Sprintf("%s(%s)", param.TypeName,
                paramVars[i].VarName())
        }
    }

    f.AddLine("return %s.%s(%s)", rcvVar.VarName(), m.Name,
        strings.Join(finalParams, ", "))

    return nil
}

func (a *api) outputDispatchers(f simprogtext.SimProgFile) {
    mCallParam := simprogtext.NewSimpleVar("mc")
    errVar := simprogtext.NewSimpleVar("err")
    argsVar := simprogtext.NewSimpleVar("arg")
    rcvVar := simprogtext.NewSimpleVar("r")

    for rcvTypeName, methods := range a.Methods {

        f.AddLineIndent(
            "func (%s *%s) Dispatch(%s *vesupro.MethodCall) " +
            "(vesupro.VesuproObject, error) {",
            rcvVar.VarName(), rcvTypeName, mCallParam.VarName())
        f.AddLine("var %s error", errVar.VarName())
        f.AddLine("%s := %s.Arguments", argsVar.VarName(),
            mCallParam.VarName())

        f.AddLine("switch %s.Name {", mCallParam.VarName())

        for _, _m := range methods {
            m := (*method)(_m)
            f.AddLineIndent("case %q:", m.Name)
            m.outputCall(f, mCallParam, argsVar, errVar, rcvVar,
                []string{"nil"})
            f.Unindent()
        }

        f.AddLineIndent("default:")
        f.AddLine(`return nil,fmt.Errorf("Unknown function %%s.", %s.Name)`,
            mCallParam.VarName())
        f.AddLineUnindent("}") // switch
        f.AddLineUnindent("}") // DispatchCall function
    }
}

func (a *api) outputPrelude(f simprogtext.SimProgFile) {
    f.AddLine("package " + a.PackageName)
    f.AddLineIndent("import (")
    f.AddLine(`"fmt"`)
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

    outText := simprogtext.NewBufferedSimProgFile(fOut)

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
