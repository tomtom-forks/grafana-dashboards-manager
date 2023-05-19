package grafana

import (
	"encoding/json"
	"fmt"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana/helpers"
	"github.com/icza/dyno"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"regexp"
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
	FolderUID string  `json:"folderUid"`
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
	UID     string `json:"uid"`
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

// DefsFile is written to disc and contains maps of a dashboard/library name -> raw Json
type DefsFile struct {
	DashboardMetaBySlug map[string]DbSearchResponse `json:"dashboardMetaBySlug"`
	DashboardBySlug     map[string]*Dashboard       `json:"-"`

	LibraryMetaByUID map[string]LibraryElementResponse `json:"libraryMetaBySlug"`
	LibraryByUID     map[string]*Library               `json:"-"`

	FoldersMetaByUID      map[string]DbSearchResponse `json:"foldersMetaByUID"`
	DashboardVersionByUID map[string]int              `json:"dashboardVersionByUID"`
	LibraryVersionByUID   map[string]int              `json:"libraryVersionByUID"`
}

// UnmarshalJSON tells the JSON parser how to unmarshal JSON data into an
// instance of the Dashboard structure.
// Returns an error if there was an issue unmarshalling the JSON.
func (d *Dashboard) UnmarshalJSON(b []byte) (err error) {
	// Define the structure of what we want to parse
	var body struct {
		Dashboard rawJSON `json:"dashboard"`
		Meta      struct {
			Version int `json:"version"`
		} `json:"meta"`
		UID string `json:"uid"`
	}

	// Unmarshal the JSON into the newly defined structure
	if err = json.Unmarshal(b, &body); err != nil {
		return
	}
	// Define all fields with their corresponding value.
	d.Version = body.Meta.Version
	d.RawJSON = body.Dashboard

	// Define the dashboard's name from the previously extracted JSON description
	d.UID, d.Name, err = UIDNameFromRawJSON(d.RawJSON)
	return
}

// UIDNameFromRawJSON finds a dashboard's name from the content of its
// RawJSON fields
func UIDNameFromRawJSON(rawJSON []byte) (UID, name string, err error) {
	// Define the necessary structure to catch the dashboard's name
	var v struct {
		Name string `json:"title"`
		UID  string `json:"uid"`
	}

	// Unmarshal the JSON content into the structure and set the dashboard's
	// name
	err = json.Unmarshal(rawJSON, &v)
	return v.UID, v.Name, err
}

var replacementForSlug = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func GetSluglikeName(UID, Title string) string {
	return UID + ":" + replacementForSlug.ReplaceAllString(Title, "_")
}

// GetDashboardsURIs requests the Grafana API for the list of all dashboards,
// then returns the dashboards' URIs. An URI will look like "uid/[UID]".
// Returns an error if there was an issue requesting the URIs or parsing the
// response body.
func (c *Client) GetDashboardsURIs() (dashboardMetaBySlug map[string]DbSearchResponse, FoldersMetaByUID map[string]DbSearchResponse, Folders []DbSearchResponse, err error) {

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

	Folders = make([]DbSearchResponse, 0)

	for _, db := range respBody {
		slug := GetSluglikeName(db.UID, db.Title)
		if db.Type == "dash-db" {
			dashboardMetaBySlug[slug] = db
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
	dashRaw := string(db.RawJSON)
	result := gjson.Get(dashRaw, "panels")
	changed := false
	for i, _ := range result.Array() {
		dashRaw, _ = sjson.Delete(dashRaw, "panels."+strconv.Itoa(i)+".libraryPanel.version")
		if dashRaw != string(db.RawJSON) {
			changed = true
			dashRaw, _ = sjson.Delete(dashRaw, "panels."+strconv.Itoa(i)+".libraryPanel.meta.created")
			dashRaw, _ = sjson.Delete(dashRaw, "panels."+strconv.Itoa(i)+".libraryPanel.meta.createdBy")
			dashRaw, _ = sjson.Delete(dashRaw, "panels."+strconv.Itoa(i)+".libraryPanel.meta.updated")
			dashRaw, _ = sjson.Delete(dashRaw, "panels."+strconv.Itoa(i)+".libraryPanel.meta.updatedBy")
		}
	}
	dashRaw, _ = sjson.Delete(dashRaw, "meta.created")
	dashRaw, _ = sjson.Delete(dashRaw, "meta.updated")
	if changed {
		var m interface{}
		err = json.Unmarshal([]byte(dashRaw), &m)
		prettyStr, _ := json.MarshalIndent(m, "", "  ")
		logrus.Debugf("rawJSON dashboard %v", string(prettyStr))
	}

	db.RawJSON = []byte(dashRaw)

	return
}

// CreateOrUpdateDashboard takes a given JSON content (as []byte) and create the
// dashboard if it doesn't exist on the Grafana instance, else updates the
// existing one. The Grafana API decides whether to create or update based on the
// "id" attribute in the dashboard's JSON: If it's unknown or null, it's a
// creation, else it's an update.
// Returns an error if there was an issue generating the request body, performing
// the request or decoding the response's body.
func (c *Client) CreateOrUpdateDashboard(contentJSON []byte, folderUID string) (err error) {
	reqBody := dbCreateOrUpdateRequest{
		Dashboard: rawJSON(contentJSON),
		Overwrite: true,
		FolderUID: folderUID,
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
	}).Debug("Removed??")
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

	var httpError *httpUnknownError
	var isHttpUnknownError bool
	// Send the request
	respBodyJSON, err := c.request(method, apiPath, reqBodyJSON)
	if err != nil {
		// Check the error against the httpUnknownError type in order to decide
		// how to process the error
		httpError, isHttpUnknownError = err.(*httpUnknownError)
		// We process httpUnknownError errors below, after we decoded the body
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
