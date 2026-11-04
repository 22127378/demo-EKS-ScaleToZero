package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// credTTL is how long a password read from Secrets Manager is reused. RDS rotates the managed master
// password every 7 days; after a rotation new connections fail for at most this long.
const credTTL = time.Minute

// credProvider hands out the database user and password and re-reads them once they are older than ttl.
// The pool asks for them every time it opens a connection, so a rotated password is picked up without a restart.
type credProvider struct {
	fetch func(ctx context.Context) (user, pass string, err error)
	ttl   time.Duration
	now   func() time.Time

	mu   sync.Mutex
	user string
	pass string
	at   time.Time
	ok   bool
}

func newCredProvider(fetch func(context.Context) (string, string, error), ttl time.Duration) *credProvider {
	return &credProvider{fetch: fetch, ttl: ttl, now: time.Now}
}

// get returns the cached credentials, or fresh ones when the cache is older than ttl. If the refresh
// fails the previous credentials are returned: a Secrets Manager hiccup must not break working connections.
func (c *credProvider) get(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.now().Sub(c.at) < c.ttl {
		return c.user, c.pass, nil
	}
	user, pass, err := c.fetch(ctx)
	if err != nil {
		if c.ok {
			return c.user, c.pass, nil
		}
		return "", "", err
	}
	c.user, c.pass, c.at, c.ok = user, pass, c.now(), true
	return user, pass, nil
}

// credentials returns the database user and password. On AWS they come from the Secrets Manager
// secret that RDS manages (DB_SECRET_ARN); locally from DB_USER and DB_PASSWORD.
func credentials(ctx context.Context) (string, string, error) {
	arn := os.Getenv("DB_SECRET_ARN")
	if arn == "" {
		return os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), nil
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", "", fmt.Errorf("load aws config: %w", err)
	}
	out, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(arn)})
	if err != nil {
		return "", "", fmt.Errorf("read secret: %w", err)
	}
	var s struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(out.SecretString)), &s); err != nil {
		return "", "", fmt.Errorf("parse secret: %w", err)
	}
	return s.Username, s.Password, nil
}
