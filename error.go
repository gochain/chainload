package chainload

import "strings"

func nonceErr(msg string) bool {
	return msg == "nonce too low"
}

func knownTxErr(msg string) bool {
	return msg == "replacement transaction underpriced" ||
		strings.HasPrefix(msg, "known transaction")
}

func lowFundsErr(msg string) bool {
	return msg == "insufficient funds for gas * price + value"
}
