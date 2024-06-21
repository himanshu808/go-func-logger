package main

import (
	"bufio"
	"fmt"
	// "github.com/sanity-io/litter"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type FuncInfo struct {
	Name        string
	Params      []string
	Returns     []string
	EntryLogPos token.Position   // only one entry point of a func
	ExitLogPos  []token.Position // there can be multiple exit points
}

type LogInfo struct {
	Log string
	Col int
}

func NewFuncInfo(fset *token.FileSet) FuncInfo {
	var zeroPos token.Pos
	zeroPos = token.NoPos

	fnInfo := FuncInfo{}

	fnInfo.Name = ""
	fnInfo.Params = nil
	fnInfo.Returns = nil
	fnInfo.EntryLogPos = fset.Position(zeroPos)
	fnInfo.ExitLogPos = nil

	return fnInfo
}

func HasField(s interface{}, field string) bool {
	r := reflect.ValueOf(s)

	if r.Kind() == reflect.Ptr {
		r = r.Elem()
	}

	if r.Kind() != reflect.Struct {
		return false
	}

	return r.FieldByName(field).IsValid()
}

func GenerateAST(fileName string) (*ast.File, *token.FileSet) {
	fset := token.NewFileSet()

	root, err := parser.ParseFile(fset, fileName, nil, parser.SkipObjectResolution)
	if err != nil {
		log.Fatal(err)
	}

	return root, fset
}

func IsFuncBodyValid(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}

	if body.Lbrace == token.NoPos || !body.Lbrace.IsValid() {
		return false
	}

	if body.Rbrace == token.NoPos || !body.Rbrace.IsValid() {
		return false
	}

	return true
}

func ExtractNameFromField(field *ast.Field) string {
	//litter.Dump(*field)

	if !HasField(field, "Names") || !HasField(field, "Type") || len(field.Names) == 0 {
		return ""
	}

	if len(field.Names) > 1 {
		log.Fatal("unknown parameter type")
	}

	return field.Names[0].Name
}

func GetParamNames(params *ast.FieldList) []string {
	var res []string

	if !HasField(params, "List") || len(params.List) == 0 {
		return res
	}

	for _, field := range params.List {
		res = append(res, ExtractNameFromField(field))
	}

	return res
}

func FindReturnStmts(fn *ast.FuncDecl, fset *token.FileSet) []token.Position {
	var res []token.Position

	ast.Inspect(fn, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if ok {
			res = append(res, fset.Position(ret.Pos()))
		}

		return true
	})

	return res
}

// second return value represents whether to ignore the first or not; ignore if False
func ExtractFuncInfo(fn *ast.FuncDecl, fset *token.FileSet) (FuncInfo, bool) {
	result := NewFuncInfo(fset)

	// ignore if function body is empty
	if !IsFuncBodyValid(fn.Body) {
		return result, false
	}

	if fn.Name != nil {
		result.Name = fn.Name.Name
	}

	if HasField(fn.Type, "Params") {
		result.Params = GetParamNames(fn.Type.Params)
	}

	if HasField(fn.Type, "Results") {
		result.Returns = GetParamNames(fn.Type.Results)
	}

	if len(fn.Body.List) == 0 {
		result.EntryLogPos = fset.Position(fn.Body.Lbrace)
	} else {
		result.EntryLogPos = fset.Position(fn.Body.List[0].Pos())
	}

	result.ExitLogPos = FindReturnStmts(fn, fset)
	// litter.Dump(result.ExitLogPos)

	lastRet := false                            // assume last stmt in func body is not a return stmt
	exitLogPos := fset.Position(fn.Body.Rbrace) // in that case, the exit log should be just before the func rbrace
	if len(fn.Body.List) != 0 {
		// get the indentation of the last statement in func body
		tempPos := fset.Position(fn.Body.List[len(fn.Body.List)-1].Pos())
		exitLogPos.Column = tempPos.Column

		_, lastRet = fn.Body.List[len(fn.Body.List)-1].(*ast.ReturnStmt)
	}

	if len(result.ExitLogPos) == 0 || !lastRet {
		// if no return stmts are found in func body
		//  OR
		// there are return stmts but the last stmt in the func body is not a return stmt

		// func A(x int) {
		//     if (x == 5) {
		//         return
		//     }
		//     fmt.Println("x was not 5")
		// }

		// making sure in such cases there is an exit log just before the ending rbrace
		result.ExitLogPos = append(result.ExitLogPos, exitLogPos)
	}

	return result, true
}

func GetAllFuncInfo(root *ast.File, fset *token.FileSet) []FuncInfo {
	var fnInfo []FuncInfo

	for _, decl := range root.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		info, ok := ExtractFuncInfo(fn, fset)
		if !ok {
			continue
		}

		//litter.Dump(info)

		fnInfo = append(fnInfo, info)
	}

	return fnInfo
}

func GetParamLog(params []string) (string, string, int) {
	paramLog := ""
	paramValLog := ""
	count := 0

	if params == nil {
		return paramLog, paramValLog, count
	}

	for _, param := range params {
		if param == "" {
			// not sure how to print unnamed param values
			continue
		}

		paramLog += param + ": " + "%+v, "
		paramValLog += param + ","
		count = count + 1
	}

	return strings.TrimSuffix(paramLog, ", "), strings.TrimSuffix(paramValLog, ","), count
}

func GetEntryLogInfo(info FuncInfo) LogInfo {
	var logInfo LogInfo

	entryLog := fmt.Sprintf("Starting func %s", info.Name)
	paramLog, paramValLog, count := GetParamLog(info.Params)

	if count != 0 {
		entryLog += fmt.Sprintf(" with values: %s", paramLog)
		logInfo.Log = fmt.Sprintf("fmt.Printf(\"%s\\n\", %s)", entryLog, paramValLog)
	} else {
		logInfo.Log = fmt.Sprintf("fmt.Println(\"%s\")", entryLog)
	}

	logInfo.Col = info.EntryLogPos.Column
	return logInfo
}

func GetExitLogInfo(info FuncInfo, idx int, line int) LogInfo {
	var logInfo LogInfo

	logInfo.Log = fmt.Sprintf("fmt.Println(\"Exiting func %s from line %d\")", info.Name, line)
	logInfo.Col = info.ExitLogPos[idx].Column

	return logInfo
}

func GenerateLogs(fnInfo []FuncInfo) map[int][]LogInfo {
	var logs map[int][]LogInfo
	logs = make(map[int][]LogInfo)

	count := 0

	for _, info := range fnInfo {
		logs[info.EntryLogPos.Line] = append(logs[info.EntryLogPos.Line], GetEntryLogInfo(info))
		count = count + 1
		for idx, exitLog := range info.ExitLogPos {
			logs[exitLog.Line] = append(logs[exitLog.Line], GetExitLogInfo(info, idx, exitLog.Line+count))
			count = count + 1
		}
	}

	return logs
}

func ReadFileLines(path string) []string {
	var contents []string

	file, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		contents = append(contents, scanner.Text())
	}

	if scanner.Err() != nil {
		log.Fatal(scanner.Err())
	}

	return contents
}

func WriteLogsToFile(path string, contents []string, logs map[int][]LogInfo) {
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}

	defer file.Close()

	wr := bufio.NewWriter(file)
	for idx, line := range contents {
		infos, ok := logs[idx+1]
		if ok {
			for _, info := range infos {
				// go uses tabs for indentation
				indent := strings.Repeat("\t", info.Col-1)
				logLine := fmt.Sprintf("%s%s", indent, info.Log)
				fmt.Fprintln(wr, logLine)
			}
		}

		fmt.Fprintln(wr, line)
	}

	err = wr.Flush()
	if err != nil {
		log.Fatal(err)
	}
}

func GetNewPath(path string) string {
	var name string
	var dir string
	var newName string

	name = filepath.Base(path)
	dir = filepath.Dir(path)

	newName = fmt.Sprintf("debug_%s", name)

	return dir + "/" + newName
}

func AddLogsToFile(root *ast.File, fset *token.FileSet, filePath string) {
	allFuncInfo := GetAllFuncInfo(root, fset)

	logs := GenerateLogs(allFuncInfo)
	newFilePath := GetNewPath(filePath)

	fmt.Printf("\n\nold path: %s, new path: %s\n\n", filePath, newFilePath)
	contents := ReadFileLines(filePath)

	WriteLogsToFile(newFilePath, contents, logs)

	fmt.Println("finished writing to file")
}

func main() {
	var fileName string
	var root *ast.File
	var fset *token.FileSet

	fileName = os.Args[2]
	root, fset = GenerateAST(fileName)

	// ast.Print(fset, root)
	AddLogsToFile(root, fset, fileName)
}
