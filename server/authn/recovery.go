package authn

import "encoding/json"

// ValidateRecoveryAccounts validates imported accounts without installing them.
func ValidateRecoveryAccounts(data []byte) error {
	var db database
	if err := json.Unmarshal(data, &db); err != nil {
		return err
	}
	return validateDatabase(&db)
}
