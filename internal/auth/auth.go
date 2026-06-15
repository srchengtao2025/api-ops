// Package auth: api-ops 内部账号系统
//
// 设计 (A 方案 - 基础账号):
//   - 3 角色: admin / finance / viewer
//   - bcrypt(cost=10) 哈希密码
//   - JWT(HS256) 24h 过期, claim 含 sub=user_id, role, pwd_ts=password_changed_at
//   - 撤销: 改密时 password_changed_at 推进, 老 JWT 失效
//   - 老 OPS_API_TOKEN 兼容: 作为 admin 等效 (过渡期, 后续用 bootstrap-admin 命令替代)
//
// 不做:
//   - 资源级权限 (单租户)
//   - 2FA / 邮件验证 (10 人内部系统不需要)
//   - 刷新 token / 短期 access + 长期 refresh
//   - 软删除密码 (永远保留)
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims JWT payload
type Claims struct {
	UserID               uint64 `json:"sub"` // 数字 sub (RFC 7519)
	Username             string `json:"username"`
	Role                 string `json:"role"`
	PwdTS                int64  `json:"pwd_ts"` // 签发时用户 password_changed_at, 服务器校驗時若 > 此值则拒
	jwt.RegisteredClaims        // iss/exp/iat
}

// Service 鉴权 service 持有 config (从 main 注入)
type Service struct {
	JWTSecret  []byte        // HS256 secret (>= 32 字节)
	TokenTTL   time.Duration // 24h
	Issuer     string        // "api-ops"
	BcryptCost int           // 默认 10
}

// NewService 构造
func NewService(secret string, ttl time.Duration) *Service {
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	if len(secret) < 32 {
		// 不足 32 字节直接补到 32 字节 (不能拒, 老 OPS_API_TOKEN 可能短)
		// 真正的强 secret 在配置层要求
		pad := make([]byte, 32)
		copy(pad, secret)
		secret = string(pad)
	}
	return &Service{
		JWTSecret:  []byte(secret),
		TokenTTL:   ttl,
		Issuer:     "api-ops",
		BcryptCost: 10,
	}
}

// HashPassword bcrypt
func (s *Service) HashPassword(plain string) (string, error) {
	if len(plain) < 8 {
		return "", errors.New("password too short (min 8 chars)")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), s.BcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword bcrypt 比较
func (s *Service) VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// IssueToken 签发 JWT
func (s *Service) IssueToken(userID uint64, username, role string, pwdChangedAt int64) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		PwdTS:    pwdChangedAt,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.Issuer,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.TokenTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.JWTSecret)
}

// ParseToken 验签 + 解析, 失败返 error
func (s *Service) ParseToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
