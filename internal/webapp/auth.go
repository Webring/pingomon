package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const initDataMaxAge = 24 * time.Hour

type TelegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func Authenticate(initData, botToken string) (*TelegramUser, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, fmt.Errorf("parse init data: %w", err)
	}

	hash := values.Get("hash")
	if hash == "" {
		return nil, fmt.Errorf("missing hash")
	}

	authDateRaw := values.Get("auth_date")
	if authDateRaw == "" {
		return nil, fmt.Errorf("missing auth_date")
	}

	authDateUnix, err := strconv.ParseInt(authDateRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid auth_date: %w", err)
	}

	authDate := time.Unix(authDateUnix, 0)
	if time.Since(authDate) > initDataMaxAge {
		return nil, fmt.Errorf("init data expired")
	}

	parts := make([]string, 0, len(values))
	for key, vals := range values {
		if key == "hash" || len(vals) == 0 {
			continue
		}
		parts = append(parts, key+"="+vals[0])
	}
	sort.Strings(parts)

	dataCheckString := strings.Join(parts, "\n")

	secretHMAC := hmac.New(sha256.New, []byte("WebAppData"))
	secretHMAC.Write([]byte(botToken))
	secret := secretHMAC.Sum(nil)

	checkHMAC := hmac.New(sha256.New, secret)
	checkHMAC.Write([]byte(dataCheckString))
	expectedHash := hex.EncodeToString(checkHMAC.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(hash), []byte(expectedHash)) != 1 {
		return nil, fmt.Errorf("invalid hash")
	}

	userRaw := values.Get("user")
	if userRaw == "" {
		return nil, fmt.Errorf("missing user")
	}

	var user TelegramUser
	if err := json.Unmarshal([]byte(userRaw), &user); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}

	return &user, nil
}
