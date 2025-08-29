module github.com/tsarna/vinculum

go 1.24.5

require github.com/tsarna/mqttpattern v0.0.0

require (
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
)

replace github.com/tsarna/mqttpattern => ../mqttpattern
