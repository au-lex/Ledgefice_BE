package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// ─── Bank list, account lookup, and Direct Debit mandates ────────────────────
// Fallback recurring-billing path for customers who paid via bank_transfer and
// have no tokenized card

type Bank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func (n *NombaService) ListBanks() ([]Bank, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", n.BaseURL+"/v1/transfers/banks", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accountId", n.AccountID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nomba list banks failed: status %d, body: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Data struct {
			Results []Bank `json:"results"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data.Results, nil
}

type BankAccountLookupResult struct {
	AccountNumber string `json:"accountNumber"`
	AccountName   string `json:"accountName"`
}

func (n *NombaService) LookupBankAccount(accountNumber, bankCode string) (*BankAccountLookupResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	payload := map[string]string{
		"accountNumber": accountNumber,
		"bankCode":      bankCode,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/transfers/bank/lookup", bytes.NewReader(body))
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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nomba bank account lookup failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Data BankAccountLookupResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

type CreateMandateInput struct {
	CustomerAccountNumber string
	BankCode              string
	CustomerName          string
	CustomerAddress       string
	CustomerAccountName   string
	Amount                float64
	Frequency             string 
	Narration             string
	CustomerPhoneNumber   string
	MerchantReference     string 
	StartDate             string // e.g. "2026-08-01T00:00"
	EndDate               string // e.g. "2027-08-01T00:00"
	CustomerEmail         string
	StartImmediately      bool
}

type CreateMandateResult struct {
	MandateID         string `json:"mandateId"`
	MerchantReference string `json:"merchantReference"`
	PhoneNumber       string `json:"phoneNumber"`
	Description       string `json:"description"` 
}

func (n *NombaService) CreateDirectDebitMandate(in CreateMandateInput) (*CreateMandateResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"customerAccountNumber": in.CustomerAccountNumber,
		"bankCode":              in.BankCode,
		"customerName":          in.CustomerName,
		"customerAddress":       in.CustomerAddress,
		"customerAccountName":   in.CustomerAccountName,
		"amount":                in.Amount,
		"frequency":             in.Frequency,
		"narration":             in.Narration,
		"customerPhoneNumber":   in.CustomerPhoneNumber,
		"merchantReference":     in.MerchantReference,
		"startDate":             in.StartDate,
		"endDate":               in.EndDate,
		"customerEmail":         in.CustomerEmail,
		"startImmediately":      in.StartImmediately,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/direct-debits", bytes.NewReader(body))
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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nomba create mandate failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Data CreateMandateResult `json:"data"`
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

// ─── Debit an active mandate (recurring charge for Direct Debit subscribers) ──

type DebitMandateResult struct {
	MandateID string `json:"mandateId"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	Message   string `json:"message"`
}

func (n *NombaService) DebitMandate(mandateID string, amount float64) (*DebitMandateResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	payload := map[string]string{
		"mandateId": mandateID,
		"amount":    fmt.Sprintf("%.2f", amount), 
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", n.BaseURL+"/v1/direct-debits/debit-mandate", bytes.NewReader(body))
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
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nomba debit mandate failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Data DebitMandateResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ─── Get mandate status ───────────────────────────────────────────────────────


type MandateStatusResult struct {
	CustomerAccountName   string `json:"customerAccountName"`
	MandateID             string `json:"mandateId"`
	CustomerAccountNumber string `json:"customerAccountNumber"`
	MandateStatus         string `json:"mandateStatus"` // e.g. "Active" — capitalized, per Nomba's actual response
	RejectionComment      string `json:"rejectionComment,omitempty"`
	MandateAdviceStatus   string `json:"mandateAdviceStatus,omitempty"`
}

func (n *NombaService) GetMandateStatus(mandateID string) (*MandateStatusResult, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/direct-debits/status?mandateId=%s", n.BaseURL, mandateID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("accountId", n.AccountID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nomba get mandate status failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Data MandateStatusResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ─── Get saved cards for a customer (after order + tokenization) ─────────────

type TokenizedCard struct {
	TokenKey            string `json:"tokenKey"`
	CustomerEmail       string `json:"customerEmail"`
	CardType            string `json:"cardType"`
	CardPan             string `json:"cardPan"`
	TokenExpirationDate string `json:"tokenExpirationDate"`
}


func (n *NombaService) GetSavedCards(customerEmail string) ([]TokenizedCard, error) {
	token, err := n.getToken()
	if err != nil {
		return nil, err
	}

	accountForLookup := n.AccountID

	var matches []TokenizedCard
	page := 0

	for {
		url := fmt.Sprintf("%s/v1/checkout/tokenized-card-data?page=%d", n.BaseURL, page)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("accountId", accountForLookup)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("nomba list tokenized cards failed: status %d, body: %s", resp.StatusCode, string(respBody))
		}

		var out struct {
			Data struct {
				NextPage               int             `json:"nextPage"`
				HasNextPage            bool            `json:"hasNextPage"`
				TokenizedCardDataList  []TokenizedCard `json:"tokenizedCardDataList"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, card := range out.Data.TokenizedCardDataList {
			if card.CustomerEmail == customerEmail {
				matches = append(matches, card)
			}
		}

		if !out.Data.HasNextPage {
			break
		}
		page = out.Data.NextPage
	}

	return matches, nil
}