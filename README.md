# GoLicenseGuard
Tool to check license (in)compatibilities.

This tool uses `go list -deps -json` to get a list of (transitive) dependencies for which Go package you run it from. It uses https://github.com/google/licensecheck to detect the code license in the file headers, the package folder, or parent folders (in this order.) 

Right now, the code will only show incompatibilities, ie. non-AGPL code that depends on AGPL code.
