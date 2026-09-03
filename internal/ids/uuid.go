package ids

import "github.com/google/uuid"

func NewString() string {
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}

	return u.String()
}
