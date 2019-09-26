package chainload

import (
	"encoding/json"
	"math/big"

	"github.com/blendle/zapdriver"
	"go.uber.org/zap"
)

var (
	senderLabel = zapdriver.Label("accountType", "sender")
	seederLabel = zapdriver.Label("accountType", "seeder")
)

// zapBig returns a Field which will encode as a
// proper JSON number, instead of a quoted string.
func zapBig(key string, b *big.Int) zap.Field {
	return zap.Reflect(key, json.Number(b.String()))
}
