package provider

import (
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// TemperatureFor returns temperature t as a request param for model, or an
// omitted param when the model rejects temperature (see models.SupportsTemperature).
// Use it for every Temperature field so retargeting a call site (for example
// models.UtilityModelName) to such a model does not turn the call into a 400.
func TemperatureFor(model string, t float64) param.Opt[float64] {
	if !models.SupportsTemperature(model) {
		return param.Opt[float64]{}
	}
	return openai.Float(t)
}
