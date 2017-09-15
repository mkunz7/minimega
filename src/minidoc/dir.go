// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"io"
	"io/ioutil"
	log "minilog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"present"
)

func init() {
	http.HandleFunc("/", dirHandler)
}

// dirHandler serves a directory listing for the requested path, rooted at basePath.
func dirHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/minimega.git") {
		// modify host to github, keep rest of the URL intact (including query params)
		url := r.URL
		url.Host = "github.com"
		url.Path = "/sandia-minimega" + url.Path
		http.Redirect(w, r, url.String(), 301)
		return
	}

	if r.URL.Path == "/favicon.ico" {
		http.Error(w, "not found", 404)
		return
	}
	const base = "."
	name := filepath.Join(base, r.URL.Path)

	if isDoc(name) {
		err := renderDoc(w, name)
		if err != nil {
			log.Errorln(err)
			http.Error(w, err.Error(), 500)
		}
		return
	}
	if isDir, err := dirList(w, name); err != nil {
		log.Errorln(err)
		http.Error(w, err.Error(), 500)
		return
	} else if isDir {
		return
	}

	// try to render .html as a template
	if filepath.Ext(name) == ".html" {
		if err := renderHTML(w, name); err != nil {
			log.Errorln(err)
			http.Error(w, err.Error(), 500)
		}
		return
	}

	http.FileServer(http.Dir(*f_root)).ServeHTTP(w, r)
}

func isDoc(path string) bool {
	_, ok := contentTemplate[filepath.Ext(path)]
	return ok
}

var (
	// dirListTemplate holds the front page template.
	dirListTemplate *template.Template

	// contentTemplate maps the presentable file extensions to the
	// template to be executed.
	contentTemplate map[string]*template.Template

	// layoutTemplate holds the page layout
	layoutTemplate *template.Template
)

func initTemplates(base string) error {
	// Locate the template file.
	actionTmpl := filepath.Join(base, "action.tmpl")

	contentTemplate = make(map[string]*template.Template)

	for ext, contentTmpl := range map[string]string{
		".slide":   "slides.tmpl",
		".article": "article.tmpl",
	} {
		contentTmpl = filepath.Join(base, contentTmpl)

		// Read and parse the input.
		tmpl := present.Template()
		tmpl = tmpl.Funcs(template.FuncMap{"playable": executable})
		if _, err := tmpl.ParseFiles(actionTmpl, contentTmpl); err != nil {
			return err
		}
		contentTemplate[ext] = tmpl
	}

	var err error
	layoutTemplate, err = template.ParseFiles(filepath.Join(base, "layout.tmpl"))
	if err != nil {
		return err
	}

	dirListTemplate, err = template.ParseFiles(filepath.Join(base, "dir.tmpl"))
	if err != nil {
		return err
	}

	tmpl, err := layoutTemplate.Clone()
	if err != nil {
		return err
	}

	dirListTemplate, err = dirListTemplate.AddParseTree("layout.tmpl", tmpl.Tree)
	return err
}

// renderDoc reads the present file, gets its template representation,
// and executes the template, sending output to w.
func renderDoc(w io.Writer, docFile string) error {
	// Read the input and build the doc structure.
	doc, err := parse(docFile, 0)
	if err != nil {
		return err
	}

	// Find which template should be executed.
	tmpl := contentTemplate[filepath.Ext(docFile)]

	// Execute the template.
	return doc.Render(w, tmpl)
}

// renderHTML parses the html file as a template and tries to execute it with
// layoutTemplate. Reparses the html file each time.
func renderHTML(w io.Writer, name string) error {
	log.Info("renderHTML: %v", name)

	f := filepath.Join(*f_root, name)
	tmpl, err := layoutTemplate.Clone()
	if err != nil {
		return err
	}

	tmpl, err = tmpl.ParseFiles(f)
	if err != nil {
		return err
	}

	return tmpl.Execute(w, nil)
}

func parse(name string, mode present.ParseMode) (*present.Doc, error) {
	f, err := os.Open(filepath.Join(*f_root, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return present.Parse(f, name, 0)
}

// dirList scans the given path and writes a directory listing to w.
// It parses the first part of each .slide file it encounters to display the
// presentation title in the listing.
// If the given path is not a directory, it returns (isDir == false, err == nil)
// and writes nothing to w.
func dirList(w io.Writer, name string) (isDir bool, err error) {
	f, err := os.Open(filepath.Join(*f_root, name))
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if isDir = fi.IsDir(); !isDir {
		return false, nil
	}
	fis, err := f.Readdir(0)
	if err != nil {
		return false, err
	}
	d := &dirListData{Path: name}
	for _, fi := range fis {
		// skip the pkg directory
		if name == "." && fi.Name() == "pkg" {
			continue
		}
		e := dirEntry{
			Name: fi.Name(),
			Path: filepath.ToSlash(filepath.Join(name, fi.Name())),
		}
		// If there's an index.html, send that back and bail out
		if fi.Name() == "index.html" {
			// returning true is naughty but whatever
			return true, renderHTML(w, e.Path)
		}

		if fi.IsDir() && showDir(e) {
			d.Dirs = append(d.Dirs, e)
			continue
		}
		if isDoc(e.Name) {
			if p, err := parse(e.Path, present.TitlesOnly); err != nil {
				log.Errorln(err)
			} else {
				e.Title = p.Title
			}
			switch filepath.Ext(e.Path) {
			case ".article":
				d.Articles = append(d.Articles, e)
			case ".slide":
				d.Slides = append(d.Slides, e)
			}
		} else if showFile(e.Name) {
			d.Other = append(d.Other, e)
		}
	}
	if d.Path == "." {
		d.Path = ""
	}
	sort.Sort(d.Dirs)
	sort.Sort(d.Slides)
	sort.Sort(d.Articles)
	sort.Sort(d.Other)
	return true, dirListTemplate.Execute(w, d)
}

// showFile reports whether the given file should be displayed in the list.
func showFile(n string) bool {
	switch filepath.Ext(n) {
	case ".pdf":
	case ".html":
	case ".go":
	default:
		return isDoc(n)
	}
	return true
}

// showDir reports whether the given directory should be displayed in the list.
func showDir(e dirEntry) bool {
	n := e.Name
	if len(n) > 0 && (n[0] == '.' || n[0] == '_') || n == "present" {
		return false
	}

	// make sure the directory has at least one displayed file
	files, err := ioutil.ReadDir(filepath.Join(*f_root, e.Path))
	if err != nil {
		return false
	}
	for _, f := range files {
		if showFile(f.Name()) {
			return true
		}
	}

	return false
}

type dirListData struct {
	Path                          string
	Dirs, Slides, Articles, Other dirEntrySlice
}

type dirEntry struct {
	Name, Path, Title string
}

type dirEntrySlice []dirEntry

func (s dirEntrySlice) Len() int           { return len(s) }
func (s dirEntrySlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s dirEntrySlice) Less(i, j int) bool { return s[i].Name < s[j].Name }
