package grafana

import (
	"encoding/json"
	"fmt"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana/helpers"
	"github.com/icza/dyno"
	"github.com/sirupsen/logrus"
	"strconv"
)

// DbSearchResponse represents an element of the response to a dashboard search
// query
type DbSearchResponse struct {
	ID        int      `json:"id"`
	Title     string   `json:"title"`
	URI       string   `json:"uri"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags"`
	Starred   bool     `json:"isStarred"`
	UID       string   `json:"uid"`
	FolderUID string   `json:"folderUid,omitEmpty"`
	FolderID  int      `json:"folderId,omitEmpty"`
}

// dbCreateOrUpdateRequest represents the request sent to create or update a
// dashboard
type dbCreateOrUpdateRequest struct {
	Dashboard rawJSON `json:"dashboard"`
	Overwrite bool    `json:"overwrite"`
	FolderID  int     `json:"folderId"`
}

// dbCreateOrUpdateResponse represents the response sent by the Grafana API to
// a dashboard creation or update. All fields described from the Grafana
// documentation aren't located in this structure because there are some we
// don't need.
type dbCreateOrUpdateResponse struct {
	Status  string `json:"success"`
	Version int    `json:"version,omitempty"`
	Message string `json:"message,omitempty"`
}

// Dashboard represents a Grafana dashboard, with its JSON definition, slug and
// current version.
type Dashboard struct {
	RawJSON []byte
	Name    string
	Slug    string
	Version int
}

type Folder struct {
	Title     string   `json:"title"`
	UID       string   `json:"uid"`
	URI       string   `json:"uri"`
	Tags      []string `json:"tags"`
	Starred   bool     `json:"isStarred"`
	FolderUID string   `json:"folderUid,omitEmpty"`
}

type DashboardVersion struct {
	Meta DbSearchResponse
}

type VersionFile struct {
	DashboardMetaByTitle map[string]DbSearchResponse `json:"dashboardMetaByTitle"`
	DashboardMetaBySlug  map[string]DbSearchResponse `json:"dashboardMetaBySlug"`
	DashboardBySlug      map[string]*Dashboard       `json:"-"`

	FoldersMetaByUID       map[string]DbSearchResponse `json:"foldersMetaByUID"`
	DashboardVersionBySlug map[string]int              `json:"dashboardVersionBySlug"`
}

// UnmarshalJSON tells the JSON parser how to unmarshal JSON data into an
// instance of the Dashboard structure.
// Returns an error if there was an issue unmarshalling the JSON.
func (d *Dashboard) UnmarshalJSON(b []byte) (err error) {
	// Define the structure of what we want to parse
	var body struct {
		Dashboard rawJSON `json:"dashboard"`
		Meta      struct {
			Slug    string `json:"slug"`
			Version int    `json:"version"`
		} `json:"meta"`
	}

	// Unmarshal the JSON into the newly defined structure
	if err = json.Unmarshal(b, &body); err != nil {
		return
	}
	// Define all fields with their corresponding value.
	d.Slug = body.Meta.Slug
	d.Version = body.Meta.Version
	d.RawJSON = body.Dashboard

	// Define the dashboard's name from the previously extracted JSON description
	err = d.setDashboardNameFromRawJSON()
	return
}

// setDashboardNameFromJSON finds a dashboard's name from the content of its
// RawJSON field
func (d *Dashboard) setDashboardNameFromRawJSON() (err error) {
	// Define the necessary structure to catch the dashboard's name
	var dashboard struct {
		Name string `json:"title"`
	}

	// Unmarshal the JSON content into the structure and set the dashboard's
	// name
	err = json.Unmarshal(d.RawJSON, &dashboard)
	d.Name = dashboard.Name

	return
}

// GetDashboardsURIs requests the Grafana API for the list of all dashboards,
// then returns the dashboards' URIs. An URI will look like "uid/[UID]".
// Returns an error if there was an issue requesting the URIs or parsing the
// response body.
func (c *Client) GetDashboardsURIs() (URIs []string, dashboardMetaBySlug map[string]DbSearchResponse, FoldersMetaByUID map[string]DbSearchResponse, Folders []DbSearchResponse, err error) {

	FoldersMetaByUID = make(map[string]DbSearchResponse, 0)
	dashboardMetaBySlug = make(map[string]DbSearchResponse, 0)

	resp, err := c.request("GET", "search", nil)
	if err != nil {
		return
	}

	var respBody []DbSearchResponse

	if err = json.Unmarshal(resp, &respBody); err != nil {
		return
	}

	logrus.WithFields(logrus.Fields{
		"json": string(resp),
	}).Debug("JSON")

	URIs = make([]string, 0)
	Folders = make([]DbSearchResponse, 0)

	for _, db := range respBody {
		if db.Type == "dash-db" {
			URIs = append(URIs, "uid/"+db.UID)
			dashboardMetaBySlug[string(db.Title)] = db
			logrus.WithFields(logrus.Fields{
				"db": db,
			}).Info("Dashboard metadata from grafana")
		} else if db.Type == "dash-folder" {
			Folders = append(Folders, db)
			FoldersMetaByUID[strconv.Itoa(db.ID)] = db
			logrus.WithFields(logrus.Fields{
				"db": db,
			}).Info("Folder metadata from grafana")
		} else {
			logrus.WithFields(logrus.Fields{
				"db": db,
			}).Warn("Unknown metadata from grafana")
		}
	}
	return
}

// GetDashboard requests the Grafana API for a dashboard identified by a given
// URI (using the same format as GetDashboardsURIs).
// Returns the dashboard as an instance of the Dashboard structure.
// Returns an error if there was an issue requesting the dashboard or parsing
// the response body.
func (c *Client) GetDashboard(URI string) (db *Dashboard, err error) {
	body, err := c.request("GET", "dashboards/"+URI, nil)
	if err != nil {
		return
	}

	db = new(Dashboard)
	err = json.Unmarshal(body, db)
	return
}

// CreateOrUpdateDashboard takes a given JSON content (as []byte) and create the
// dashboard if it doesn't exist on the Grafana instance, else updates the
// existing one. The Grafana API decides whether to create or update based on the
// "id" attribute in the dashboard's JSON: If it's unkown or null, it's a
// creation, else it's an update.
// Returns an error if there was an issue generating the request body, performing
// the request or decoding the response's body.
func (c *Client) CreateOrUpdateDashboard(contentJSON []byte, folderID int) (err error) {
	reqBody := dbCreateOrUpdateRequest{
		Dashboard: rawJSON(contentJSON),
		Overwrite: true,
		FolderID:  folderID,
	}

	// Generate the request body's JSON
	reqBodyJSON, err := json.Marshal(reqBody)

	var v interface{}
	if err := json.Unmarshal([]byte(reqBodyJSON), &v); err != nil {
		panic(err)
	}
	idv1, _ := dyno.Get(v, "dashboard/id")

	err2 := dyno.Set(v, nil, "dashboard", "id")
	idv, err3 := dyno.Get(v, "dashboard", "id")
	dyno.Delete(v, "__folderUID")

	reqBodyJSON, err = json.Marshal(v)
	logrus.WithFields(logrus.Fields{
		//		"reqBodyJson":    string(reqBodyJSON),
		"err2": err2,
		"idv":  idv,
		"idv1": idv1,
		"err3": err3,
	}).Info("Removed??")
	if err != nil {
		return
	}
	err = c.createOrUpdateDashboardFolder(reqBodyJSON, contentJSON, "dashboards/db")
	return
}

func (c *Client) createOrUpdateDashboardFolder(reqBodyJSON []byte, contentJSON []byte, apiPath string) (err error) {
	err = c.createOrUpdateDashboardFolderMethod(reqBodyJSON, contentJSON, apiPath, "POST")
	return
}

func (c *Client) createOrUpdateDashboardFolderMethod(reqBodyJSON []byte, contentJSON []byte, apiPath string, method string) (err error) {

	var httpError *httpUnkownError
	var isHttpUnknownError bool
	// Send the request
	respBodyJSON, err := c.request(method, apiPath, reqBodyJSON)
	if err != nil {
		// Check the error against the httpUnkownError type in order to decide
		// how to process the error
		httpError, isHttpUnknownError = err.(*httpUnkownError)
		// We process httpUnkownError errors below, after we decoded the body
		if !isHttpUnknownError {
			return
		}
	}

	// Decode the response body
	var respBody dbCreateOrUpdateResponse
	if err = json.Unmarshal(respBodyJSON, &respBody); err != nil {
		return
	}

	if respBody.Status != "success" && isHttpUnknownError {
		// Get the dashboard/folders's slug for logging
		var slug string
		slug, err = helpers.GetSlug(contentJSON)
		if err != nil {
			return
		}

		return fmt.Errorf(
			"Failed to update %s %s (%d %s): %s req: %s",
			apiPath, slug, httpError.StatusCode, respBody.Status, respBody.Message, reqBodyJSON,
		)
	}

	return
}

// DeleteDashboard deletes the dashboard identified by a given slug on the
// Grafana API.
// Returns an error if the process failed.
func (c *Client) DeleteDashboard(slug string) (err error) {
	_, err = c.request("DELETE", "dashboards/db/"+slug, nil)
	return
}
