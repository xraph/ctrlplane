package managed

import "encoding/json"

// AttemptFenceFactDigest returns the retained removal-fence fact digest.
// Callers obtain the fence from an authenticated attempt binding read. Computing
// this digest does not validate the fence or confer mutation authority.
func AttemptFenceFactDigest(fence AttemptFenceRecord) (string, error) {
	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{"removal_fence", fence})
	if err != nil {
		return "", err
	}

	return Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...)), nil
}
