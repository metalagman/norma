package execmodel

import "go.uber.org/fx"

// Module is the Fx module for the execmodel package.
var Module = fx.Module("execmodel",
	fx.Provide(
		New,
	),
)
