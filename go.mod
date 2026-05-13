module github.com/millken/goapp-template

go 1.24.4

require (
	github.com/joho/godotenv v1.5.1
	github.com/millken/inertia v1.0.2
	github.com/millken/inertia/middleware v1.0.2
	github.com/millken/inertia/ssr v1.0.2
	github.com/millken/inertia/ssr/quickjs v1.0.2
	github.com/phuslu/log v1.0.124
	github.com/spf13/cobra v1.10.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/buke/quickjs-go v0.7.6 // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/dop251/goja v0.0.0-20260311135729-065cd970411c // indirect
	github.com/dop251/goja_nodejs v0.0.0-20260212111938-1f56ff5bcf14 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/text v0.16.0 // indirect
)

replace (
	github.com/millken/inertia => ../inertia
	github.com/millken/inertia/middleware => ../inertia/middleware
	github.com/millken/inertia/ssr => ../inertia/ssr
	github.com/millken/inertia/ssr/quickjs => ../inertia/ssr/quickjs
)
