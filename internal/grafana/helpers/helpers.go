package helpers

import (
	"encoding/json"

	"github.com/gosimple/slug"
)

// GetSlug reads the JSON description of a dashboard or folder and computes a
// slug from its title.
// Returns an error if there was an issue parsing the dashboard JSON description.
func GetSlug(dbJSONDescription []byte) (dbSlug string, err error) {
	// Parse the file's content to find the dashboard's title
	var thingTitle struct {
		Title string `json:"title"`
	}

	err = json.Unmarshal(dbJSONDescription, &thingTitle)
	// Compute the slug
	dbSlug = slug.Make(thingTitle.Title)
	return
}

