#!/usr/bin/env sh
# Print the licence notices for everything linked into or shipped with pepe for
# the current GOOS/GOARCH. Run after ./build.sh (module cache and fonts present).
set -eu
ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

notice() {
	printf '================================================================================\n%s\n================================================================================\n\n' "$1"
	cat -- "$2"
	printf '\n'
}

# Distribution packages (e.g. Arch) move the licence out of GOROOT.
for go_license in "$(go env GOROOT)/LICENSE" /usr/share/licenses/go/LICENSE; do
	[ ! -f "$go_license" ] || break
done
[ -f "$go_license" ] || { printf 'Go licence not found\n' >&2; exit 1; }
notice 'Go standard library and runtime' "$go_license"
notice 'Roboto font' internal/app/embed/roboto-OFL.txt
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' . | sort -u |
	while read -r module dir; do
		found=''
		for file in "$dir"/*; do
			case "${file##*/}" in
				LICENSE|LICENSE.md|LICENSE.txt|LICENCE|COPYING|UNLICENSE) notice "$module (${file##*/})" "$file"; found=1 ;;
			esac
		done
		[ -n "$found" ] || { printf 'No licence file found for %s\n' "$module" >&2; exit 1; }
	done
