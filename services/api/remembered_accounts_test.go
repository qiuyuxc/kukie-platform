package main

import (
	"bytes"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func rememberReader(test *testing.T, fixture fixture, email string) map[string]any {
	test.Helper()
	return call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]any{"email": email, "password": readerPassword, "client": "android", "remember": true}, 200)
}

func quickRequest(token string) map[string]string {
	return map[string]string{"remember_token": token}
}

func TestRememberedLoginLifecycle(test *testing.T) {
	fixture := newFixture(test)
	createReader(test, fixture, "remember@example.test")
	signed := rememberReader(test, fixture, "remember@example.test")
	token, credential := signed["token"].(string), signed["remember_token"].(string)
	var stored string
	if err := fixture.app.db.QueryRow("SELECT hash FROM remembered_accounts").Scan(&stored); err != nil || stored != digest(credential) || stored == credential {
		test.Fatal("quick credential must only be stored as a hash", err)
	}
	call(test, fixture, "GET", "/api/v1/me", credential, nil, 401)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(token), 401)
	call(test, fixture, "POST", "/api/v1/auth/logout", token, map[string]any{}, 200)
	call(test, fixture, "GET", "/api/v1/me", token, nil, 401)
	resumed := call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(credential), 200)
	if resumed["remember_token"] == credential || resumed["token"] == token || resumed["remember_expires_at"] != signed["remember_expires_at"] {
		test.Fatal("quick login must rotate both tokens without extending the device expiry")
	}
	if resumed["user"].(map[string]any)["id"] != signed["user"].(map[string]any)["id"] {
		test.Fatal("quick login changed account")
	}
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(credential), 401)
	call(test, fixture, "GET", "/api/v1/me", resumed["token"].(string), nil, 200)
	credential = resumed["remember_token"].(string)
	call(test, fixture, "POST", "/api/v1/auth/forget", "", quickRequest(credential), 200)
	call(test, fixture, "POST", "/api/v1/auth/forget", "", quickRequest(credential), 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(credential), 401)
}

func TestRememberedLoginConsentAndReplacement(test *testing.T) {
	fixture := newFixture(test)
	createReader(test, fixture, "consent@example.test")
	input := map[string]any{"email": "consent@example.test", "password": readerPassword, "client": "android"}
	result := call(test, fixture, "POST", "/api/v1/auth/login", "", input, 200)
	if _, exists := result["remember_token"]; exists {
		test.Fatal("credential issued without consent")
	}
	input["remember"] = true
	input["client"] = "web"
	result = call(test, fixture, "POST", "/api/v1/auth/login", "", input, 200)
	if _, exists := result["remember_token"]; exists {
		test.Fatal("credential issued to web client")
	}
	signed := rememberReader(test, fixture, "consent@example.test")
	input["client"], input["remember"], input["remember_token"] = "android", false, signed["remember_token"]
	call(test, fixture, "POST", "/api/v1/auth/login", "", input, 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(signed["remember_token"].(string)), 401)
}

func TestRegistrationCanRememberAccount(test *testing.T) {
	fixture := newFixture(test)
	email, code := "new-remember@example.test", "123456"
	_, err := fixture.app.db.Exec("INSERT INTO email_codes(email,hash,expires) VALUES(?,?,?)", email, fixture.app.keyed("email:"+email+":"+code), time.Now().Add(time.Minute).Unix())
	if err != nil {
		test.Fatal(err)
	}
	signed := call(test, fixture, "POST", "/api/v1/auth/register", "", map[string]any{"email": email, "name": "新读者", "password": readerPassword, "code": code, "client": "android", "remember": true}, 200)
	call(test, fixture, "POST", "/api/v1/auth/logout", signed["token"].(string), map[string]any{}, 200)
	resumed := call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(signed["remember_token"].(string)), 200)
	if resumed["user"].(map[string]any)["email"] != email {
		test.Fatal("registration remembered a different account")
	}
}

func TestRememberedLoginRateLimit(test *testing.T) {
	fixture := newFixture(test)
	for attempt := 0; attempt < 30; attempt++ {
		call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(randomToken()), 401)
	}
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(randomToken()), 429)
}

func TestRememberedLoginUsesCurrentProfile(test *testing.T) {
	fixture := newFixture(test)
	createReader(test, fixture, "profile-quick@example.test")
	signed := rememberReader(test, fixture, "profile-quick@example.test")
	call(test, fixture, "PATCH", "/api/v1/me", signed["token"].(string), map[string]string{"name": "修改后的昵称", "bio": "修改后的简介"}, 200)
	resumed := call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(signed["remember_token"].(string)), 200)
	account := resumed["user"].(map[string]any)
	if account["name"] != "修改后的昵称" || account["bio"] != "修改后的简介" {
		test.Fatal("quick login returned stale profile")
	}
}

func TestRememberedLogoutForgetIsScoped(test *testing.T) {
	fixture := newFixture(test)
	createReader(test, fixture, "first@example.test")
	createReader(test, fixture, "second@example.test")
	first := rememberReader(test, fixture, "first@example.test")
	otherDevice := rememberReader(test, fixture, "first@example.test")
	second := rememberReader(test, fixture, "second@example.test")
	call(test, fixture, "POST", "/api/v1/auth/logout", first["token"].(string), map[string]any{"forget": true, "remember_token": second["remember_token"]}, 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(second["remember_token"].(string)), 200)
	call(test, fixture, "POST", "/api/v1/auth/logout", otherDevice["token"].(string), map[string]any{"forget": true, "remember_token": otherDevice["remember_token"]}, 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(otherDevice["remember_token"].(string)), 401)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(first["remember_token"].(string)), 200)
}

func TestRememberedLoginExpiryAndConcurrentRotation(test *testing.T) {
	fixture := newFixture(test)
	createReader(test, fixture, "race@example.test")
	signed := rememberReader(test, fixture, "race@example.test")
	credential := signed["remember_token"].(string)
	var workers sync.WaitGroup
	statuses := make(chan int, 2)
	for attempt := 0; attempt < 2; attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request := httptest.NewRequest("POST", "/api/v1/auth/quick-login", bytes.NewBufferString(`{"remember_token":"`+credential+`"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	workers.Wait()
	first, second := <-statuses, <-statuses
	if !(first == 200 && second == 401 || first == 401 && second == 200) {
		test.Fatalf("concurrent token reuse: %d, %d", first, second)
	}
	signed = rememberReader(test, fixture, "race@example.test")
	credential = signed["remember_token"].(string)
	if _, err := fixture.app.db.Exec("UPDATE remembered_accounts SET expires=? WHERE hash=?", time.Now().Unix()-1, digest(credential)); err != nil {
		test.Fatal(err)
	}
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(credential), 401)
}

func TestRememberedLoginTwoFactor(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "factor-quick@example.test")
	before := rememberReader(test, fixture, "factor-quick@example.test")
	setup := call(test, fixture, "POST", "/api/v1/me/2fa/setup", reader, map[string]string{"password": readerPassword}, 200)
	code, err := totp.GenerateCode(setup["secret"].(string), time.Now())
	if err != nil {
		test.Fatal(err)
	}
	enabled := call(test, fixture, "POST", "/api/v1/me/2fa/enable", reader, map[string]string{"code": code}, 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(before["remember_token"].(string)), 401)
	codes := enabled["recovery_codes"].([]any)
	signed := call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]any{"email": "factor-quick@example.test", "password": readerPassword, "otp": codes[0], "client": "android", "remember": true}, 200)
	input := quickRequest(signed["remember_token"].(string))
	if result := call(test, fixture, "POST", "/api/v1/auth/quick-login", "", input, 401); result["code"] != "totp_required" {
		test.Fatal("quick login bypassed second factor")
	}
	input["otp"] = "invalid"
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", input, 401)
	input["otp"] = codes[1].(string)
	resumed := call(test, fixture, "POST", "/api/v1/auth/quick-login", "", input, 200)
	input["remember_token"] = resumed["remember_token"].(string)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", input, 401)
	call(test, fixture, "POST", "/api/v1/me/2fa/disable", resumed["token"].(string), map[string]any{"password": readerPassword, "code": codes[2]}, 200)
	call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(resumed["remember_token"].(string)), 401)
}

func TestRememberedCredentialsRevokedByAccountChanges(test *testing.T) {
	for _, change := range []string{"password", "email", "delete"} {
		test.Run(change, func(test *testing.T) {
			fixture := newFixture(test)
			reader := createReader(test, fixture, "change@example.test")
			signed := rememberReader(test, fixture, "change@example.test")
			switch change {
			case "password":
				call(test, fixture, "PUT", "/api/v1/me/password", reader, map[string]string{"password": readerPassword, "new_password": "a-new-safe-reader-password"}, 200)
			case "email":
				userID := readerUserID(test, fixture, reader)
				code, email := "123456", "changed@example.test"
				_, err := fixture.app.db.Exec("INSERT INTO email_changes(user_id,email,hash,expires) VALUES(?,?,?,?)", userID, email, fixture.app.keyed("email-change:"+userID+":"+email+":"+code), time.Now().Add(time.Minute).Unix())
				if err != nil {
					test.Fatal(err)
				}
				call(test, fixture, "PUT", "/api/v1/me/email", reader, map[string]string{"password": readerPassword, "email": email, "code": code}, 200)
			case "delete":
				call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": readerPassword, "confirm": "注销我的账号"}, 200)
			}
			call(test, fixture, "POST", "/api/v1/auth/quick-login", "", quickRequest(signed["remember_token"].(string)), 401)
		})
	}
}
