package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/licensecheck"
	"github.com/pkg/errors"
)

var ErrNoLicense = fmt.Errorf("no license found")

func findLicenseFile(dir string) (string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		lower := strings.ToLower(entry.Name())
		switch lower {
		case "copying", "copying.md", "copying.markdown", "copying.txt", // this list is from https://pkg.go.dev/license-policy
			"licence", "licence.md", "licence.markdown", "licence.txt",
			"license", "license.md", "license.markdown", "license.txt",
			"license-2.0.txt", "licence-2.0.txt", "license-apache", "licence-apache",
			"license-apache-2.0.txt", "licence-apache-2.0.txt", "license-mit", "licence-mit",
			"license.mit", "licence.mit", "license.code", "licence.code",
			"license.docs", "licence.docs", "license.rst", "licence.rst",
			"mit-license", "mit-licence", "mit-license.md", "mit-licence.md",
			"mit-license.markdown", "mit-licence.markdown", "mit-license.txt", "mit-licence.txt",
			"mit_license", "mit_licence", "unlicense", "unlicence",
			"license_apache2": // used by grafana/loki
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", ErrNoLicense
}

func findLicenseFileUp(dir string) (string, error) {
	for {
		lic, err := findLicenseFile(dir)
		if err != nil {
			if err != ErrNoLicense {
				return "", err
			}
		} else {
			return lic, nil
		}
		dir = filepath.Dir(dir)
		if !strings.Contains(dir, "@") {
			break
		}
	}
	return "", ErrNoLicense
}

type ImportPath string

func normalizeImportPath(importPath string) ImportPath {
	return ImportPath(strings.TrimPrefix(importPath, "vendor/"))
}

var licenseIdCache = map[string]string{} // file -> license ID mapping

func (p *Package) findLicense() (string, error) {
	if p.license != "" {
		return p.license, nil
	}
	if p.Standard {
		return "standard", nil
	}
	if p.ForTest != "" {
		return "test", nil
	}

	// Check whether (all) the source files contain a license header
	licenseId, err := findLicenseHeaders(p.Dir, p.GoFiles)
	if err != nil {
		// Look for a LICENSE* file in the package directory (or parents) instead
		licenseFile, err := findLicenseFileUp(p.Dir)
		if err != nil {
			return "", errors.Wrapf(err, "finding license file for %s", p.ImportPath)
		}

		licenseId = licenseIdCache[licenseFile]
		if licenseId == "" { // not in cache
			licenseId, err = ReadLicenseFile(licenseFile)
			if err != nil {
				return "", err
			}
			licenseIdCache[licenseFile] = licenseId
		}
	}

	p.license = licenseId
	return licenseId, nil
}

func findLicenseHeaders(dir string, files []string) (string, error) {
	licenseIds := map[string]int{}
	for _, file := range files {
		license, err := ReadLicenseFile(filepath.Join(dir, file))
		if err != nil {
			return "", err // bail on first error (eg. file without license)
		}
		licenseIds[license]++
	}
	for licenseId, _ := range licenseIds {
		return licenseId, nil // TODO: handle multiple licenses
	}
	return "", ErrNoLicense
}

func ReadLicenseFile(licenseFile string) (string, error) {
	license, err := os.ReadFile(licenseFile)
	if err != nil {
		return "", errors.Wrapf(err, "reading license file %s", licenseFile)
	}
	cov := licensecheck.Scan(license)
	if len(cov.Match) == 0 {
		return "", errors.Wrapf(ErrNoLicense, "scanning license file %s", licenseFile)
	}
	return cov.Match[0].ID, nil // TODO: handle multiple licenses
}

// Package represents a Go package. This (partial) definition is copied from the `go help list` command.
type Package struct {
	Dir        string   // directory containing package sources
	ImportPath string   // import path of package in dir
	Imports    []string // import paths used by this package
	ForTest    string   // package is only for use in named test
	Deps       []string // all (recursively) imported dependencies
	Standard   bool     // is this package part of the standard Go library?
	GoFiles    []string // .go source files (excluding CgoFiles, TestGoFiles, XTestGoFiles)

	license string
}

// getPackageDependencies returns a list of dependencies for the current module, including their paths and directories
func getPackageDependencies() ([]Package, error) {
	out, err := exec.Command("go", "list", "-deps", "-json").Output()
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(strings.NewReader(string(out)))
	var packages []Package

	for {
		var mod Package
		if err := decoder.Decode(&mod); err != nil {
			break
		}
		packages = append(packages, mod)
	}

	return packages, nil
}

func main() {
	// Step 1: Get the list of dependencies
	deps, err := getPackageDependencies()
	if err != nil {
		panic(err)
	}

	// Step 2: Iterate over dependencies and read LICENSE file
	byImportPath := map[ImportPath]*Package{}
	depOf := map[ImportPath][]ImportPath{}
	importOf := map[ImportPath][]ImportPath{}

	for _, dep := range deps {
		// make a copy of dep on the heap
		pdep := new(Package)
		*pdep = dep
		importPath := normalizeImportPath(dep.ImportPath)
		byImportPath[importPath] = pdep
		if dep.ForTest != "" || dep.Standard {
			continue
		}
		for _, d := range dep.Deps {
			pkg := normalizeImportPath(d)
			depOf[pkg] = append(depOf[pkg], importPath)
		}
		for _, d := range dep.Imports {
			pkg := normalizeImportPath(d)
			importOf[pkg] = append(importOf[pkg], importPath)
		}
	}

	// Step 3: Check for license compatibility
	var issues int
	for importPath, p := range byImportPath {
		lic, _ := p.findLicense()
		if strings.Contains(lic, "AGPL") {
			continue
		}

		var found bool
		for _, imp := range p.Imports {
			pkg := normalizeImportPath(imp)
			p := byImportPath[pkg]
			if p == nil || p.Standard || p.ForTest != "" {
				continue
			}
			depLic, _ := p.findLicense()
			if strings.Contains(depLic, "AGPL") {
				if !found {
					fmt.Printf("%s licensed package %s using packages:\n", lic, importPath)
					issues++
				}
				found = true
				fmt.Printf("  imports %s (%s)\n", pkg, depLic)
			}
		}
	}

	if issues > 0 {
		os.Exit(1)
	}
}
