package notify

// Provider is a mail service the e-mail channel knows: the page fills in its
// server and says which sign-in it takes. Values from the providers' help
// pages (checked 2026-11).
type Provider struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Security string `json:"security"`
	// Auth are the sign-ins the service takes, the one to use first.
	Auth []string `json:"auth"`
	// OAuth is the sign-in service of the "oauth2" sign-in.
	OAuth string `json:"oauth,omitempty"`
	// Tenant is the Microsoft directory a sign-in uses by default.
	Tenant string `json:"tenant,omitempty"`
}

const (
	oauthGoogle    = "google"
	oauthMicrosoft = "microsoft"
)

// Providers are in the page's order; "custom" is a server of the user's own.
var Providers = []Provider{
	{ID: "gmail", Host: "smtp.gmail.com", Port: "587", Security: "starttls", Auth: []string{"oauth2", "password"}, OAuth: oauthGoogle},
	{ID: "microsoft365", Host: "smtp.office365.com", Port: "587", Security: "starttls", Auth: []string{"oauth2", "password"}, OAuth: oauthMicrosoft, Tenant: "organizations"},
	{ID: "outlook", Host: "smtp-mail.outlook.com", Port: "587", Security: "starttls", Auth: []string{"oauth2"}, OAuth: oauthMicrosoft, Tenant: "consumers"},
	{ID: "yahoo", Host: "smtp.mail.yahoo.com", Port: "465", Security: "tls", Auth: []string{"password"}},
	{ID: "icloud", Host: "smtp.mail.me.com", Port: "587", Security: "starttls", Auth: []string{"password"}},
	{ID: "zoho", Host: "smtp.zoho.com", Port: "465", Security: "tls", Auth: []string{"password"}},
	{ID: "fastmail", Host: "smtp.fastmail.com", Port: "465", Security: "tls", Auth: []string{"password"}},
	{ID: "yandex", Host: "smtp.yandex.com", Port: "465", Security: "tls", Auth: []string{"password"}},
	{ID: "mailru", Host: "smtp.mail.ru", Port: "465", Security: "tls", Auth: []string{"password"}},
	{ID: "proton", Host: "smtp.protonmail.ch", Port: "587", Security: "starttls", Auth: []string{"password"}},
	{ID: "custom", Auth: []string{"password"}},
}

func providerByID(id string) Provider {
	for _, p := range Providers {
		if p.ID == id {
			return p
		}
	}
	return Providers[len(Providers)-1]
}

func providerIDs() []string {
	ids := make([]string, 0, len(Providers))
	for _, p := range Providers {
		ids = append(ids, p.ID)
	}
	return ids
}
