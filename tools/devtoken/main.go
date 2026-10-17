// Command devtoken issues an HS256 JWT for local testing. The service itself only validates tokens.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	secret := flag.String("secret", "local-dev-secret-change-me-32-bytes-min", "HS256 secret, must match jwt.secret")
	sub := flag.String("sub", "dev-user", "subject claim")
	iss := flag.String("iss", "companies-local", "issuer claim")
	aud := flag.String("aud", "companies", "audience claim")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	flag.Parse()

	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   *sub,
		Issuer:    *iss,
		Audience:  jwt.ClaimStrings{*aud},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(*ttl)),
	}).SignedString([]byte(*secret))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(token)
}
