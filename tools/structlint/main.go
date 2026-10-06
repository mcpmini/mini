package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/mcpmini/mini/tools/structlint/analyzer"
)

func main() {
	singlechecker.Main(analyzer.Analyzer)
}
