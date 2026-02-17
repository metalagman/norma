package genaischema

import "go.uber.org/fx"

// Module is the Fx module for the genaischema package.
var Module = fx.Module("genaischema",
	fx.Provide(
		// Providing functions directly if needed, or a wrapper struct.
		// For now, we'll just define the module.
	),
)
