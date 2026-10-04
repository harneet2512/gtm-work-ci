package knowledgestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Service is the knowledge read model behind GET /knowledge and GET /knowledge/{knowledge_id}
// (WP24, HAR-122): it returns the stored object as the JSON its contract schema describes.
type Service struct {
	db *sql.DB
}

// uuidRe is the contract's uuid shape; a path segment that is not one can never name a row.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// New returns the knowledge read service over db.
func New(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("knowledgestore: a database is required")
	}
	return &Service{db: db}, nil
}

// List answers GET /knowledge: {"items": [...]} of full knowledge.v1 objects ordered by the
// numeric part of the human key. An empty status lists all; limit bounds the list (<=0: no bound).
func (s *Service) List(ctx context.Context, status string, limit int) ([]byte, error) {
	items, err := List(ctx, s.db, status, limit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Items []knowledge.Knowledge `json:"items"`
	}{Items: items})
}

// Doc answers GET /knowledge/{knowledge_id}. A malformed id is ErrNotFound, never a SQL error.
func (s *Service) Doc(ctx context.Context, id string) ([]byte, error) {
	if !uuidRe.MatchString(id) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	k, err := Get(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(k)
}
