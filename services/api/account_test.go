package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

const readerPassword = "a-safe-reader-password"

func loginReader(test *testing.T, fixture fixture, email, password string) string {
	test.Helper()
	return call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": password, "client": "android"}, 200)["token"].(string)
}

func readerUserID(test *testing.T, fixture fixture, token string) string {
	test.Helper()
	return call(test, fixture, "GET", "/api/v1/me", token, nil, 200)["user"].(map[string]any)["id"].(string)
}

func TestPasswordChangeAndSessionRevocation(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "password@example.test")
	otherSession := loginReader(test, fixture, "password@example.test", readerPassword)
	input := map[string]string{"password": "wrong", "new_password": "a-new-safe-reader-password"}
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 401)
	input["password"] = readerPassword
	input["new_password"] = "short"
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 400)
	input["new_password"] = "a-new-safe-reader-password"
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 200)
	call(test, fixture, "GET", "/api/v1/me", reader, nil, 200)
	call(test, fixture, "GET", "/api/v1/me", otherSession, nil, 401)
	call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]string{"email": "password@example.test", "password": readerPassword, "client": "android"}, 401)
	loginReader(test, fixture, "password@example.test", input["new_password"])
}

func TestAccountSecondFactorRequired(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "factor@example.test")
	id := readerUserID(test, fixture, reader)
	secret := "JBSWY3DPEHPK3PXP"
	sealed, err := fixture.app.seal([]byte(secret))
	if err != nil {
		test.Fatal(err)
	}
	if _, err = fixture.app.db.Exec("UPDATE users SET totp_secret=? WHERE id=?", sealed, id); err != nil {
		test.Fatal(err)
	}
	input := map[string]string{"password": readerPassword, "new_password": "a-new-safe-reader-password"}
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 401)
	input["otp"] = "invalid"
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 401)
	input["otp"], err = totp.GenerateCode(secret, time.Now())
	if err != nil {
		test.Fatal(err)
	}
	call(test, fixture, "PUT", "/api/v1/me/password", reader, input, 200)
	result := call(test, fixture, "GET", "/api/v1/me", reader, nil, 200)
	if result["user"].(map[string]any)["two_factor"] != true {
		test.Fatal("password change disabled two factor")
	}
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": input["new_password"], "otp": input["otp"], "confirm": "注销我的账号"}, 401)
	recovery := "ABCD-EFGH"
	if _, err = fixture.app.db.Exec("INSERT INTO recovery_codes(user_id,hash) VALUES(?,?)", id, fixture.app.keyed("recovery:"+recovery)); err != nil {
		test.Fatal(err)
	}
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": input["new_password"], "otp": recovery, "confirm": "注销我的账号"}, 200)
}

func TestEmailChangeVerification(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "before@example.test")
	otherSession := loginReader(test, fixture, "before@example.test", readerPassword)
	otherReader := createReader(test, fixture, "other@example.test")
	var recipient, code string
	fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, body string) error {
		recipient = to
		code = regexp.MustCompile(`[0-9]{6}`).FindString(body)
		return nil
	}
	call(test, fixture, "POST", "/api/v1/me/email-code", reader, map[string]string{"email": "after@example.test", "password": "wrong"}, 401)
	if code != "" {
		test.Fatal("unverified password sent email")
	}
	call(test, fixture, "POST", "/api/v1/me/email-code", reader, map[string]string{"email": " After@example.test ", "password": readerPassword}, 200)
	if recipient != "after@example.test" || code == "" {
		test.Fatal("new email verification not delivered")
	}
	input := map[string]string{"email": recipient, "password": readerPassword, "code": code}
	call(test, fixture, "PUT", "/api/v1/me/email", otherReader, input, 400)
	call(test, fixture, "POST", "/api/v1/auth/register", "", map[string]string{"email": recipient, "name": "不能复用", "password": readerPassword, "code": code, "client": "android"}, 400)
	result := call(test, fixture, "PUT", "/api/v1/me/email", reader, input, 200)
	if result["user"].(map[string]any)["email"] != recipient {
		test.Fatal("updated email not returned")
	}
	call(test, fixture, "GET", "/api/v1/me", otherSession, nil, 401)
	call(test, fixture, "PUT", "/api/v1/me/email", reader, input, 400)
	call(test, fixture, "POST", "/api/v1/auth/login", "", map[string]string{"email": "before@example.test", "password": readerPassword}, 401)
	loginReader(test, fixture, recipient, readerPassword)
}

func TestEmailChangeLimitsAndDeliveryFailure(test *testing.T) {
	for _, scenario := range []string{"failed delivery", "expired", "five attempts", "email became occupied", "SMTP disabled"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newFixture(test)
			reader := createReader(test, fixture, "source@example.test")
			id := readerUserID(test, fixture, reader)
			var code string
			fixture.app.sendMail = func(ctx context.Context, config smtpSettings, to, subject, body string) error {
				code = regexp.MustCompile(`[0-9]{6}`).FindString(body)
				if scenario == "failed delivery" {
					return errors.New("simulated delivery failure")
				}
				return nil
			}
			if scenario == "SMTP disabled" {
				if err := fixture.app.storeSetting("smtp", smtpSettings{}); err != nil {
					test.Fatal(err)
				}
				call(test, fixture, "POST", "/api/v1/me/email-code", reader, map[string]string{"email": "target@example.test", "password": readerPassword}, 503)
				return
			}
			if scenario == "failed delivery" {
				call(test, fixture, "POST", "/api/v1/me/email-code", reader, map[string]string{"email": "target@example.test", "password": readerPassword}, 502)
				var count int
				if err := fixture.app.db.QueryRow("SELECT COUNT(*) FROM email_changes").Scan(&count); err != nil || count != 0 {
					test.Fatal("failed delivery retained a usable code")
				}
				return
			}
			call(test, fixture, "POST", "/api/v1/me/email-code", reader, map[string]string{"email": "target@example.test", "password": readerPassword}, 200)
			input := map[string]string{"email": "target@example.test", "password": readerPassword, "code": code}
			expected := 400
			switch scenario {
			case "expired":
				if _, err := fixture.app.db.Exec("UPDATE email_changes SET expires=0 WHERE user_id=?", id); err != nil {
					test.Fatal(err)
				}
			case "five attempts":
				input["code"] = "wrong"
				for attempt := 0; attempt < 5; attempt++ {
					call(test, fixture, "PUT", "/api/v1/me/email", reader, input, 400)
				}
				input["code"] = code
			case "email became occupied":
				createReader(test, fixture, "target@example.test")
				expected = 409
			}
			call(test, fixture, "PUT", "/api/v1/me/email", reader, input, expected)
			if call(test, fixture, "GET", "/api/v1/me", reader, nil, 200)["user"].(map[string]any)["email"] != "source@example.test" {
				test.Fatal("failed verification changed email")
			}
		})
	}
}

func TestAccountDeletionAndMediaCleanup(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "delete@example.test")
	other := createReader(test, fixture, "keep@example.test")
	id := readerUserID(test, fixture, reader)
	postID := publishedPost(test, fixture)
	call(test, fixture, "POST", "/api/v1/posts/"+postID+"/comments", reader, map[string]string{"text": "即将删除"}, 201)
	call(test, fixture, "POST", "/api/v1/posts/"+postID+"/comments", other, map[string]string{"text": "必须保留"}, 201)
	call(test, fixture, "PUT", "/api/v1/me/bookmarks/"+postID, reader, nil, 200)
	call(test, fixture, "PUT", "/api/v1/me/likes/"+postID, reader, nil, 200)
	call(test, fixture, "PUT", "/api/v1/me/subscription", reader, map[string]any{"enabled": true, "frequency": "daily"}, 200)
	mediaID := randomToken()
	path := mediaID + ".jpg"
	config := storageSettings{Active: "local"}
	if err := fixture.app.putImage(context.Background(), config, path, "image/jpeg", []byte("avatar")); err != nil {
		test.Fatal(err)
	}
	snapshot, _ := json.Marshal(config)
	sealed, err := fixture.app.seal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	if _, err = fixture.app.db.Exec("INSERT INTO media(id,path,config,mime,size,owner_id,created_at) VALUES(?,?,?,?,?,?,?)", mediaID, path, sealed, "image/jpeg", 6, id, time.Now().Format(time.RFC3339)); err != nil {
		test.Fatal(err)
	}
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": readerPassword}, 400)
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": "wrong", "confirm": "注销我的账号"}, 401)
	call(test, fixture, "DELETE", "/api/v1/me", reader, map[string]string{"password": readerPassword, "confirm": "注销我的账号"}, 200)
	call(test, fixture, "GET", "/api/v1/me", reader, nil, 401)
	call(test, fixture, "GET", "/api/v1/me", other, nil, 200)
	for _, table := range []string{"sessions", "bookmarks", "likes", "subscriptions", "email_changes", "recovery_codes"} {
		var count int
		if err := fixture.app.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id=?", id).Scan(&count); err != nil || count != 0 {
			test.Fatalf("account data retained in %s: %d, %v", table, count, err)
		}
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, httptest.NewRequest("GET", "/media/"+mediaID, nil))
	if response.Code != 404 {
		test.Fatal("deleted avatar still publicly accessible")
	}
	comments := call(test, fixture, "GET", "/api/v1/posts/"+postID+"/comments", "", nil, 200)["comments"].([]any)
	if len(comments) != 1 || comments[0].(map[string]any)["text"] != "必须保留" {
		test.Fatal("comment cleanup crossed account boundary")
	}
	if err := fixture.app.cleanupMedia(context.Background(), time.Now()); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fixture.app.directory, "uploads", path)); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("local avatar object not removed")
	}
	call(test, fixture, "DELETE", "/api/v1/me", fixture.adminToken, map[string]string{"password": "a-safe-admin-password", "confirm": "注销我的账号"}, 403)
}

func TestAccountMutationsRequireCookieCSRF(test *testing.T) {
	fixture := newFixture(test)
	reader := createReader(test, fixture, "csrf@example.test")
	for _, mutation := range []struct{ method, path string }{{"PUT", "/api/v1/me/password"}, {"POST", "/api/v1/me/email-code"}, {"PUT", "/api/v1/me/email"}, {"DELETE", "/api/v1/me"}, {"PUT", "/api/v1/me/subscription"}} {
		request := httptest.NewRequest(mutation.method, mutation.path, strings.NewReader("{}"))
		request.AddCookie(&http.Cookie{Name: "kukie_session", Value: reader})
		request.Header.Set("Origin", "http://localhost:8085")
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != 403 {
			test.Fatalf("CSRF bypass on %s: %d", mutation.path, response.Code)
		}
	}
}
