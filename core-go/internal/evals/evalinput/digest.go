package evalinput

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// StateDigest is the sha-256 of the account state document an evaluation read. The send records it with the state
// version (send_eval_results) and the recomputation read compares it with the digest of the state the run read, so
// "the state was preserved" rests on a stored comparison, never on an assumption. Both sides marshal the same
// reducer.AccountState, so the digest is stable.
func StateDigest(s reducer.AccountState) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("evalinput: encode the account state: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
