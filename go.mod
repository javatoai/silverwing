module silverwing-go

go 1.27.3

tool github.com/egoist/mygo/cmd/mygo

// v0.10.0 with the native patches documented in third_party/MYGO-PATCHES.md.
replace github.com/egoist/mygo => ./third_party/mygo

require (
	github.com/ebitengine/purego v0.11.1
	github.com/egoist/mygo v0.10.0
	github.com/go-text/typesetting v0.3.5
	github.com/makiuchi-d/gozxing v0.1.1
	github.com/yuin/goldmark v1.8.6
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/image v0.46.0
	golang.org/x/net v0.59.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
)

require (
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/cloudflare/circl v1.6.2 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	go.digitalxero.dev/rpm v0.3.4 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
)
