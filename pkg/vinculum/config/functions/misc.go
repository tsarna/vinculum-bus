package functions

import (
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// TypeOfFunc returns the friendly name of the type of a given value
var TypeOfFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "value", Type: cty.DynamicPseudoType},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		return cty.StringVal(args[0].Type().FriendlyName()), nil
	},
})
