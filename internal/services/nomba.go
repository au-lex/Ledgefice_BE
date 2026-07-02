package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

type NombaService struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	AccountID    string // parent account — goes in the accountId HEADER
	SubAccountID string // sub-account — goes inside order.accountId in the BODY

	token       string
	tokenExpiry time.Time
	mu          sync.Mutex
}

func NewNombaService() *NombaService {
	return &NombaService{
		BaseURL:      os.Getenv("NOMBA_BASE_URL"),
		ClientID:     os.Getenv("NOMBA_CLIENT_ID"),
		ClientSecret: os.Getenv("NOMBA_CLIENT_SECRET"),
		AccountID:    os.Getenv("NOMBA_ACCOUNT_ID"),
		SubAccountID: os.Getenv("NOMBA_SUBACCOUNT_ID"),
	}
}

type nombaTokenResponse struct {
	Data struct {
		AccessToken string `json:"access_token"`
	} `json:"data"`
}

func (n *NombaService) getToken() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.token != "" && time.Now().Before(n.tokenExpiry) {
		return n.token, nil
	}

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     n.ClientID,
		"client_secret": n.ClientSecret,
	})

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/auth/token/issue", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("accountId", n.AccountID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nomba auth failed: status %d", resp.StatusCode)
	}

	var out nombaTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}

	n.token = out.Data.AccessToken
	n.tokenExpiry = time.Now().Add(28 * time.Minute) // buffer before real 30min expiry
	return n.token, nil
}

// ─── Create Checkout Order ────────────────────────────────────────────────────

type CheckoutOrderInput struct {
	OrderReference string
	CustomerEmail  string
	Amount         float64
	Currency       string
	CallbackURL    string
	TokenizeCard   bool
}

type CheckoutOrderResult struct {
	CheckoutLink   string `json:"checkoutLink"`
	OrderReference string `json:"orderReference"`
}

func (n *NombaService) CreateCheckoutOrder(in CheckoutOrderInput) (*CheckoutOrderResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	order := map[string]any{
		"orderReference": in.OrderReference,
		"customerEmail":  in.CustomerEmail,
		"amount":         in.Amount,
		"currency":       in.Currency,
		"callbackUrl":    in.CallbackURL,
	}
	if n.SubAccountID != "" {
		order["accountId"] = n.SubAccountID
	}

	payload := map[string]any{
		"order":        order,
		"tokenizeCard": in.TokenizeCard,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/checkout/order", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accountId", n.AccountID) // parent account ID — auth scope, not the sub-account
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nomba checkout failed: status %d", resp.StatusCode)
	}

	var out struct {
		Data CheckoutOrderResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ─── Charge a tokenized card (for renewals, after the first successful payment) ──

type TokenizedChargeInput struct {
	OrderReference string
	CustomerEmail  string
	Amount         float64
	Currency       string
	CallbackURL    string
	TokenKey       string
}

type TokenizedChargeResult struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
}

func (n *NombaService) ChargeTokenizedCard(in TokenizedChargeInput) (*TokenizedChargeResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	order := map[string]any{
		"orderReference": in.OrderReference,
		"customerEmail":  in.CustomerEmail,
		"amount":         in.Amount,
		"currency":       in.Currency,
		"callbackUrl":    in.CallbackURL,
	}
	if n.SubAccountID != "" {
		order["accountId"] = n.SubAccountID
	}

	payload := map[string]any{
		"order":    order,
		"tokenKey": in.TokenKey,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/checkout/tokenized-card-payment", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accountId", n.AccountID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nomba tokenized charge failed: status %d", resp.StatusCode)
	}

	var out struct {
		Data TokenizedChargeResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ─── Delete a tokenized card (customer wants their saved card removed) ───────

func (n *NombaService) DeleteTokenizedCard(tokenKey string) error {
	token, err := n.getToken()
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", n.BaseURL+"/v1/checkout/tokenized-card-data/"+tokenKey, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accountId", n.AccountID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nomba delete tokenized card failed: status %d", resp.StatusCode)
	}
	return nil
}

// ─── Get saved cards for a customer (after order + tokenization) ─────────────

type TokenizedCard struct {
	TokenKey            string `json:"tokenKey"`
	CustomerEmail       string `json:"customerEmail"`
	CardType            string `json:"cardType"`
	CardPan             string `json:"cardPan"`
	TokenExpirationDate string `json:"tokenExpirationDate"`
}

func (n *NombaService) GetSavedCards(orderReference string) ([]TokenizedCard, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", n.BaseURL+"/v1/checkout/user-card/"+orderReference, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nomba get saved cards failed: status %d", resp.StatusCode)
	}

	var out struct {
		Data struct {
			TokenizedCardData []TokenizedCard `json:"tokenizedCardData"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data.TokenizedCardData, nil
}