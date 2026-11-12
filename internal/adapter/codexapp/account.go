package codexapp

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// signedInEmail is the email of the ChatGPT login in the account's
// auth.json: the email claim of tokens.id_token. The token is only decoded,
// never verified; nothing here is trusted beyond showing who is signed in,
// and no token leaves this function. "" when signed out, signed in with an
// API key, or unreadable.
//
// tokens.account_id beside it is the ChatGPT account (workspace) id, which
// the members of a team workspace share, so it cannot tell two logins apart;
// the email can.
func signedInEmail(env map[string]string) string {
	data, err := os.ReadFile(filepath.Join(codexHome(env), "auth.json"))
	if err != nil {
		return ""
	}
	var auth struct {
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return ""
	}
	parts := strings.Split(auth.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Email
}
