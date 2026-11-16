package auth

import "strings"

// ValidateStrong is the administrator password rule used when the account
// is created from the setup wizard. A long passphrase qualifies, and so
// does a shorter password that mixes case, a digit and a symbol.
func ValidateStrong(username, password string) error {
	if len(password) > maxPassword {
		return &InputError{"Use a password of at most 72 characters."}
	}
	if len(password) < 12 {
		return &InputError{"Use at least 12 characters."}
	}
	lowered := strings.ToLower(password)
	if strings.EqualFold(password, username) || strings.Contains(lowered, strings.ToLower(username)) && username != "" && len(username) > 2 {
		return &InputError{"Don't include the username in the password."}
	}
	switch lowered {
	case "password", "password1", "password123", "feldcore", "admin", "admin123", "qwerty", "qwerty123", "letmein", "changeme", "correct horse":
		return &InputError{"That password is too easy to guess."}
	}
	var lower, upper, digit, symbol bool
	for _, r := range password {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	if len(password) >= 16 && lower && (upper || digit || symbol) {
		return nil
	}
	if lower && upper && digit && symbol {
		return nil
	}
	return &InputError{"Use upper and lower case, a number, and a symbol. A passphrase of 16 or more characters is fine too."}
}
