package jwt

import (
	"campuscommunity/internal/conf"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Myclaims struct {
	UserId int64 `json:"user_id"`
	jwt.RegisteredClaims
}

func keyFunc(token *jwt.Token) (i any, err error) {
	return []byte(conf.Conf.JWTConfig.Secret), nil
}

func GenToken(userID int64) (string, error) {
	claim := Myclaims{
		UserId: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(conf.Conf.JWTConfig.Expire)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    conf.Conf.JWTConfig.Issuer,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
	return token.SignedString([]byte(conf.Conf.JWTConfig.Secret))
}

func ParseToken(tokenString string) (*Myclaims, error) {
	var mc = new(Myclaims)
	token, err := jwt.ParseWithClaims(tokenString, mc, keyFunc, jwt.WithIssuer(conf.Conf.JWTConfig.Issuer))
	if err != nil {
		return nil, err
	}
	if token.Valid {
		return mc, nil
	}
	return nil, errors.New("invalid token")
}
