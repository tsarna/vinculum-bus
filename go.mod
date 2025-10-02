module github.com/tsarna/vinculum-bus

go 1.24.5

require go.uber.org/zap v1.27.0

require (
	github.com/amir-yaghoubi/mqttpattern v0.0.0-20250829083210-f7d8d46a786e
	github.com/stretchr/testify v1.11.1
	github.com/tsarna/go-structdiff v0.2.0
	go.opentelemetry.io/otel v1.28.0
	go.opentelemetry.io/otel/metric v1.28.0
	go.opentelemetry.io/otel/trace v1.28.0
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/tsarna/hcl-jqfunc => ../hcl-jqfunc
