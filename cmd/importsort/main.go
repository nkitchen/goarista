// Copyright (C) 2017  Arista Networks, Inc.
// Use of this source code is governed by the Apache License 2.0
// that can be found in the COPYING file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/vcs"
)

// Implementation taken from "isStandardImportPath" in go's source.
func isStdLibPath(path string) bool {
	i := strings.Index(path, "/")
	if i < 0 {
		i = len(path)
	}
	elem := path[:i]
	return !strings.Contains(elem, ".")
}

// sortImports takes in an "import" body and returns it sorted
func sortImports(in []byte, sections []string) []byte {
	type importLine struct {
		index int    // index into inLines
		path  string // import path used for sorting
	}
	// imports holds all the import lines, separated by section. The
	// first section is for stdlib imports, the following sections
	// hold the user specified sections, the final section is for
	// everything else.
	imports := make([][]importLine, len(sections)+2)
	addImport := func(section, index int, importPath string) {
		imports[section] = append(imports[section], importLine{index, importPath})
	}
	stdlib := 0
	offset := 1
	other := len(imports) - 1

	inLines := bytes.Split(in, []byte{'\n'})
	for i, l := range inLines {
		if len(l) == 0 {
			continue
		}
		start := bytes.IndexByte(l, '"')
		if start == -1 {
			continue
		}
		if comment := bytes.Index(l, []byte("//")); comment > -1 && comment < start {
			continue
		}

		start++ // skip '"'
		end := bytes.IndexByte(l[start:], '"') + start
		s := string(l[start:end])

		found := false
		for j, sect := range sections {
			if strings.HasPrefix(s, sect) && (len(sect) == len(s) || s[len(sect)] == '/') {
				addImport(j+offset, i, s)
				found = true
				break
			}
		}

		if !found {
			if isStdLibPath(s) {
				addImport(stdlib, i, s)
			} else {
				addImport(other, i, s)
			}
		}
	}

	out := make([]byte, 0, len(in)+2)
	needSeperator := false
	for _, section := range imports {
		if len(section) == 0 {
			continue
		}
		if needSeperator {
			out = append(out, '\n')
		}
		sort.Slice(section, func(a, b int) bool {
			return section[a].path < section[b].path
		})
		for _, s := range section {
			out = append(out, inLines[s.index]...)
			out = append(out, '\n')
		}
		needSeperator = true
	}

	return out
}

func genFile(in []byte, sections []string) []byte {
	out := make([]byte, 0, len(in)+3) // Add some fudge to avoid re-allocation

	for {
		const importLine = "\nimport (\n"
		const importLineLen = len(importLine)
		importStart := bytes.Index(in, []byte(importLine))
		if importStart == -1 {
			break
		}
		// Save to `out` everything up to and including "import(\n"
		out = append(out, in[:importStart+importLineLen]...)
		in = in[importStart+importLineLen:]
		importLen := bytes.Index(in, []byte("\n)\n"))
		if importLen == -1 {
			panic("file missing close ')'")
		}
		// Sort body of "import" and write it to `out`
		out = append(out, sortImports(in[:importLen], sections)...)
		out = append(out, []byte(")")...)
		in = in[importLen+2:]
	}
	// Write everything leftover to out
	out = append(out, in...)
	return out
}

// returns true if the file changed
func processFile(filename string, writeFile, listDiffFiles bool, sections []string) bool {
	in, err := ioutil.ReadFile(filename)
	if err != nil {
		panic(err)
	}
	out := genFile(in, sections)

	equal := bytes.Equal(in, out)
	if listDiffFiles {
		return !equal
	}
	if !writeFile {
		os.Stdout.Write(out)
		return !equal
	}

	if equal {
		return false
	}
	temp, err := ioutil.TempFile(filepath.Dir(filename), filepath.Base(filename))
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(temp.Name())
	s, err := os.Stat(filename)
	if err != nil {
		panic(err)
	}
	if _, err = temp.Write(out); err != nil {
		panic(err)
	}
	if err := temp.Close(); err != nil {
		panic(err)
	}
	err = os.Chmod(temp.Name(), s.Mode())
	if err != nil {
		panic(err)
	}

	if err := os.Rename(temp.Name(), filename); err != nil {
		panic(err)
	}

	return true
}

func vcsRootImportPath(f string) (string, error) {
	path, err := filepath.Abs(f)
	if err != nil {
		return "", err
	}
	gopath := build.Default.GOPATH
	var root string
	_, root, err = vcs.FromDir(filepath.Dir(path), filepath.Join(gopath, "src"))
	if err != nil {
		return "", err
	}
	return root, nil
}

func main() {
	writeFile := flag.Bool("w", false, "write result to file instead of stdout")
	listDiffFiles := flag.Bool("l", false, "list files whose formatting differs from importsort")
	var sections csv
	flag.Var(&sections, "s", "comma-seperated list of prefixes to define import sections,"+
		` ex: "cvshub.com/company". Default value is to use repository information.`)

	flag.Parse()
	for _, f := range flag.Args() {
		if len(sections.vals) == 0 {
			// Note: the vcs root will only be searched for on the
			// first file, because later on section.vals will be non-empty
			root, err := vcsRootImportPath(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error determining VCS root: %s", err)
			} else {
				sections.vals = append(sections.vals, root)
			}
		}
		diff := processFile(f, *writeFile, *listDiffFiles, sections.vals)
		if *listDiffFiles && diff {
			fmt.Println(f)
		}
	}
}

// csv is a comma-separated flag.Value
type csv struct {
	vals []string
}

func (c *csv) String() string {
	return strings.Join(c.vals, ", ")
}
func (c *csv) Set(s string) error {
	c.vals = strings.Split(s, ",")
	return nil
}
