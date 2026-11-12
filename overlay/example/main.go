package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env"
	"github.com/jfk9w-go/based"
	"github.com/pkg/errors"

	"github.com/jfk9w-go/lkdr-api/v2"
)

type jsonTokenStorage struct {
	path string
	legacyPhone string
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
		if _, flat := root["token"]; flat && phone == s.legacyPhone {
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

type consoleAuthorizer struct{ reader *bufio.Reader }

func newConsoleAuthorizer() *consoleAuthorizer {
	return &consoleAuthorizer{reader: bufio.NewReader(os.Stdin)}
}

func (a *consoleAuthorizer) GetCaptchaToken(_ context.Context, userAgent, siteKey, pageURL string) (string, error) {
	fmt.Println("Для новой авторизации открой страницу ФНС в браузере и реши CAPTCHA:")
	fmt.Println(pageURL)
	fmt.Println("Site key:", siteKey)
	fmt.Println("User-Agent:", userAgent)
	fmt.Print("Вставь полученный CAPTCHA-токен: ")
	line, err := a.reader.ReadString('\n')
	if err != nil { return "", errors.Wrap(err, "read CAPTCHA token") }
	token := strings.TrimSpace(line)
	if token == "" { return "", errors.New("CAPTCHA token is empty") }
	return token, nil
}

func (a *consoleAuthorizer) GetConfirmationCode(_ context.Context, phone string) (string, error) {
	fmt.Printf("Введи SMS-код для %s: ", phone)
	line, err := a.reader.ReadString('\n')
	if err != nil { return "", errors.Wrap(err, "read SMS code") }
	code := strings.TrimSpace(line)
	if code == "" { return "", errors.New("SMS code is empty") }
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

func loadExport(path string) (*exportFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) { return nil, nil }
		return nil, errors.Wrap(err, "read previous export")
	}
	var result exportFile
	if err := json.Unmarshal(data, &result); err != nil { return nil, errors.Wrap(err, "decode previous export") }
	if result.Receipts == nil { result.Receipts = make([]exportedReceipt, 0) }
	return &result, nil
}

func exportReceipts(ctx context.Context, client *lkdr.Client, output, phone string) error {
	const pageSize = 50
	const detailTimeout = 60 * time.Second

	result, err := loadExport(output)
	if err != nil { return err }
	if result == nil {
		result = &exportFile{
			ExportedAt: based.StandardClock.Now().UTC().Format("2006-01-02T15:04:05Z"),
			Receipts: make([]exportedReceipt, 0),
		}
	}
	fmt.Printf("\nАккаунт %s\nФайл экспорта: %s\n", phone, output)

	if result.ListComplete {
		fmt.Printf("Список чеков уже загружен: %d\n", len(result.Receipts))
	} else {
		fmt.Println("Загружаю список чеков...")
		seen := make(map[string]struct{}, len(result.Receipts))
		for _, item := range result.Receipts {
			if item.Receipt.Key != "" { seen[item.Receipt.Key] = struct{}{} }
		}
		offset := 0
		for {
			if err := ctx.Err(); err != nil {
				_ = saveExport(output, result)
				return err
			}
			page, err := client.Receipt(ctx, &lkdr.ReceiptIn{
				Limit: pageSize, Offset: offset, OrderBy: "RECEIVE_DATE:DESC",
			})
			if err != nil {
				_ = saveExport(output, result)
				return errors.Wrapf(err, "get receipts page at offset %d", offset)
			}
			for _, receipt := range page.Receipts {
				if receipt.Key == "" { continue }
				if _, exists := seen[receipt.Key]; exists { continue }
				seen[receipt.Key] = struct{}{}
				result.Receipts = append(result.Receipts, exportedReceipt{Receipt: receipt})
			}
			if err := saveExport(output, result); err != nil { return err }
			offset += len(page.Receipts)
			fmt.Printf("Получено записей: %d\n", len(result.Receipts))
			if !page.HasMore || len(page.Receipts) == 0 { break }
		}
		result.ListComplete = true
		if err := saveExport(output, result); err != nil { return err }
		fmt.Printf("Список загружен: %d чеков\n", len(result.Receipts))
	}

	var successCount, errorCount, skippedCount int
	for i := range result.Receipts {
		if err := ctx.Err(); err != nil {
			_ = saveExport(output, result)
			return err
		}
		receipt := &result.Receipts[i]
		if receipt.FiscalData != nil {
			successCount++
			skippedCount++
			continue
		}
		fmt.Printf("[%d/%d] Запрашиваю подробности чека %s...\n", i+1, len(result.Receipts), receipt.Receipt.Key)
		requestCtx, cancel := context.WithTimeout(ctx, detailTimeout)
		details, requestErr := client.FiscalData(requestCtx, &lkdr.FiscalDataIn{Key: receipt.Receipt.Key})
		cancel()
		if requestErr != nil {
			receipt.FiscalData = nil
			receipt.DetailError = requestErr.Error()
			errorCount++
			fmt.Printf("  Ошибка: %v\n", requestErr)
		} else {
			receipt.FiscalData = details
			receipt.DetailError = ""
			if details != nil {
				successCount++
				fmt.Println("  Подробности получены")
			} else {
				receipt.DetailError = "пустой ответ FiscalData"
				errorCount++
				fmt.Println("  API вернул пустые подробности")
			}
		}
		if err := saveExport(output, result); err != nil { return err }
	}

	result.DetailsComplete = true
	for _, item := range result.Receipts {
		if item.FiscalData == nil { result.DetailsComplete = false; break }
	}
	if err := saveExport(output, result); err != nil { return err }
	fmt.Printf("\nИтоги для %s:\n", phone)
	fmt.Printf("Всего чеков: %d\n", len(result.Receipts))
	fmt.Printf("Успешно загружено или уже было: %d\n", successCount)
	fmt.Printf("Пропущено ранее загруженных: %d\n", skippedCount)
	fmt.Printf("Ошибок в этой попытке: %d\n", errorCount)
	fmt.Printf("Все подробности загружены: %t\n", result.DetailsComplete)
	fmt.Printf("Файл: %s\n", output)
	return nil
}

type accountConfig struct { Phone, DeviceID, UserAgent, OutputFile string }

func runAccount(ctx context.Context, account accountConfig, tokensFile, legacyPhone string) error {
	client, err := lkdr.NewClient(lkdr.ClientParams{
		Phone: account.Phone,
		Clock: based.StandardClock,
		DeviceID: account.DeviceID,
		UserAgent: account.UserAgent,
		TokenStorage: jsonTokenStorage{path: tokensFile, legacyPhone: legacyPhone},
	})
	if err != nil { return errors.Wrap(err, "create LKDR client") }
	accountCtx := lkdr.WithAuthorizer(ctx, newConsoleAuthorizer())
	return exportReceipts(accountCtx, client, account.OutputFile, account.Phone)
}

func main() {
	var config struct {
		Phone string `env:"LKDR_PHONE,required"`
		TokensFile string `env:"LKDR_TOKENS_FILE,required"`
		DeviceID string `env:"LKDR_DEVICE_ID,required"`
		UserAgent string `env:"LKDR_USER_AGENT,required"`
		OutputFile string `env:"LKDR_OUTPUT_FILE" envDefault:"receipts.json"`
		Phone2 string `env:"LKDR_PHONE_2"`
		DeviceID2 string `env:"LKDR_DEVICE_ID_2"`
		UserAgent2 string `env:"LKDR_USER_AGENT_2"`
		OutputFile2 string `env:"LKDR_OUTPUT_FILE_2" envDefault:"receipts_phone2.json"`
	}
	if err := env.Parse(&config); err != nil { panic(err) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accounts := []accountConfig{{
		Phone: config.Phone, DeviceID: config.DeviceID,
		UserAgent: config.UserAgent, OutputFile: config.OutputFile,
	}}
	if strings.TrimSpace(config.Phone2) != "" {
		deviceID2 := config.DeviceID2
		if deviceID2 == "" { deviceID2 = config.DeviceID }
		userAgent2 := config.UserAgent2
		if userAgent2 == "" { userAgent2 = config.UserAgent }
		accounts = append(accounts, accountConfig{
			Phone: config.Phone2, DeviceID: deviceID2,
			UserAgent: userAgent2, OutputFile: config.OutputFile2,
		})
	}
	seenPhones := make(map[string]struct{})
	seenFiles := make(map[string]struct{})
	for _, account := range accounts {
		if _, exists := seenPhones[account.Phone]; exists {
			fmt.Fprintf(os.Stderr, "Телефон %s указан более одного раза\n", account.Phone)
			os.Exit(2)
		}
		seenPhones[account.Phone] = struct{}{}
		if _, exists := seenFiles[filepath.Clean(account.OutputFile)]; exists {
			fmt.Fprintf(os.Stderr, "Файл экспорта %s назначен нескольким аккаунтам\n", account.OutputFile)
			os.Exit(2)
		}
		seenFiles[account.OutputFile] = struct{}{}
	}
	failed := 0
	for _, account := range accounts {
		fmt.Println("\n========================================")
		fmt.Printf("Обработка телефона: %s\n", account.Phone)
		fmt.Println("========================================")
		if err := runAccount(ctx, account, config.TokensFile, config.Phone); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "Ошибка аккаунта %s: %v\n", account.Phone, err)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\nЗавершено с ошибками. Неуспешных аккаунтов: %d\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nЭкспорт всех аккаунтов завершён.")
}
