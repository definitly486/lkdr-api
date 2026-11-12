package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env"
	"github.com/jfk9w-go/based"
	"github.com/pkg/errors"

	"github.com/jfk9w-go/lkdr-api/v2"
)

type jsonTokenStorage struct {
	path string
}

func (s jsonTokenStorage) LoadTokens(_ context.Context, phone string) (*lkdr.Tokens, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "read token file")
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, errors.Wrap(err, "decode token file")
	}

	// Accept both the phone-keyed format used by this example and the flat
	// format written by update_lkdr_token.py.
	raw, ok := root[phone]
	if !ok {
		if _, flat := root["token"]; flat {
			raw = data
			ok = true
		}
	}
	if !ok {
		return nil, nil
	}

	var tokens lkdr.Tokens
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, errors.Wrap(err, "decode stored tokens")
	}
	return &tokens, nil
}

func (s jsonTokenStorage) UpdateTokens(_ context.Context, phone string, tokens *lkdr.Tokens) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return errors.Wrap(err, "create token directory")
	}

	root := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(s.path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return errors.Wrap(err, "decode existing token file")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrap(err, "read existing token file")
	}

	// Migrate an old flat token file into the phone-keyed format.
	if _, flat := root["token"]; flat {
		delete(root, "token")
		delete(root, "refreshToken")
		delete(root, "tokenExpireIn")
		delete(root, "refreshTokenExpiresIn")
	}

	if tokens == nil {
		delete(root, phone)
	} else {
		encoded, err := json.Marshal(tokens)
		if err != nil {
			return errors.Wrap(err, "encode tokens")
		}
		root[phone] = encoded
	}

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode token storage")
	}
	encoded = append(encoded, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tokens-*.tmp")
	if err != nil {
		return errors.Wrap(err, "create temporary token file")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "set token file permissions")
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "write token file")
	}
	if err := tmp.Close(); err != nil {
		return errors.Wrap(err, "close token file")
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return errors.Wrap(err, "replace token file")
	}
	return nil
}

type consoleAuthorizer struct{}

func (a *consoleAuthorizer) GetCaptchaToken(_ context.Context, userAgent, siteKey, pageURL string) (string, error) {
	fmt.Println("Для новой авторизации открой страницу ФНС в браузере и реши CAPTCHA:")
	fmt.Println(pageURL)
	fmt.Println("Site key:", siteKey)
	fmt.Println("User-Agent:", userAgent)
	fmt.Print("Вставь полученный CAPTCHA-токен: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", errors.Wrap(err, "read CAPTCHA token")
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("CAPTCHA token is empty")
	}
	return token, nil
}

func (a *consoleAuthorizer) GetConfirmationCode(_ context.Context, phone string) (string, error) {
	fmt.Printf("Введи SMS-код для %s: ", phone)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", errors.Wrap(err, "read SMS code")
	}
	code := strings.TrimSpace(line)
	if code == "" {
		return "", errors.New("SMS code is empty")
	}
	return code, nil
}

type exportedReceipt struct {
	Receipt    lkdr.Receipt       `json:"receipt"`
	FiscalData *lkdr.FiscalDataOut `json:"fiscalData,omitempty"`
	DetailError string             `json:"detailError,omitempty"`
}

type exportFile struct {
	ExportedAt      string            `json:"exportedAt"`
	ListComplete    bool              `json:"listComplete"`
	DetailsComplete bool              `json:"detailsComplete"`
	ReceiptCount    int               `json:"receiptCount"`
	Receipts        []exportedReceipt `json:"receipts"`
}

func saveExport(path string, data *exportFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.Wrap(err, "create export directory")
	}
	data.ReceiptCount = len(data.Receipts)
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode export")
	}
	encoded = append(encoded, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".receipts-*.tmp")
	if err != nil {
		return errors.Wrap(err, "create temporary export")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "set export permissions")
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return errors.Wrap(err, "write export")
	}
	if err := tmp.Close(); err != nil {
		return errors.Wrap(err, "close export")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errors.Wrap(err, "replace export")
	}
	return nil
}

func exportReceipts(ctx context.Context, client *lkdr.Client, output string) error {
	const pageSize = 50

	result := &exportFile{
		ExportedAt:      based.StandardClock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		DetailsComplete: true,
		Receipts:        make([]exportedReceipt, 0),
	}
	seen := make(map[string]struct{})
	offset := 0

	for {
		page, err := client.Receipt(ctx, &lkdr.ReceiptIn{
			Limit:   pageSize,
			Offset:  offset,
			OrderBy: "RECEIVE_DATE:DESC",
		})
		if err != nil {
			return errors.Wrapf(err, "get receipts page at offset %d", offset)
		}

		for _, receipt := range page.Receipts {
			if receipt.Key == "" {
				continue
			}
			if _, exists := seen[receipt.Key]; exists {
				continue
			}
			seen[receipt.Key] = struct{}{}
			result.Receipts = append(result.Receipts, exportedReceipt{Receipt: receipt})
		}

		// Persist the receipt list while paging, so an interruption does not
		// discard already downloaded data.
		if err := saveExport(output, result); err != nil {
			return err
		}

		offset += len(page.Receipts)
		if !page.HasMore || len(page.Receipts) == 0 {
			break
		}
	}

	result.ListComplete = true
	if err := saveExport(output, result); err != nil {
		return err
	}

	for i := range result.Receipts {
		receipt := &result.Receipts[i]
		details, err := client.FiscalData(ctx, &lkdr.FiscalDataIn{Key: receipt.Receipt.Key})
		if err != nil {
			receipt.DetailError = err.Error()
			result.DetailsComplete = false
		} else {
			receipt.FiscalData = details
			receipt.DetailError = ""
		}

		if err := saveExport(output, result); err != nil {
			return err
		}
		fmt.Printf("Обработано подробностей: %d/%d\n", i+1, len(result.Receipts))
	}

	return nil
}

func main() {
	var config struct {
		Phone      string `env:"LKDR_PHONE,required"`
		TokensFile string `env:"LKDR_TOKENS_FILE,required"`
		DeviceID   string `env:"LKDR_DEVICE_ID,required"`
		UserAgent  string `env:"LKDR_USER_AGENT,required"`
		OutputFile string `env:"LKDR_OUTPUT_FILE" envDefault:"receipts.json"`
	}
	if err := env.Parse(&config); err != nil {
		panic(err)
	}

	client, err := lkdr.NewClient(lkdr.ClientParams{
		Phone:        config.Phone,
		Clock:        based.StandardClock,
		DeviceID:     config.DeviceID,
		UserAgent:    config.UserAgent,
		TokenStorage: jsonTokenStorage{path: config.TokensFile},
	})
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = lkdr.WithAuthorizer(ctx, &consoleAuthorizer{})

	fmt.Println("Начинаю загрузку чеков и подробностей...")
	if err := exportReceipts(ctx, client, config.OutputFile); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка экспорта:", err)
		os.Exit(1)
	}
	fmt.Println("Экспорт завершён:", config.OutputFile)
}
