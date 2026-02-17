package modelfactory

import "go.uber.org/fx"

// Module is the Fx module for the modelfactory package.
var Module = fx.Module("modelfactory",
	fx.Provide(
		NewFactory,
	),
)
