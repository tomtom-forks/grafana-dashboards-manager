package grafana

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/sjson"
)

type LibraryElementResponse struct {
	Id          int    `json:"id"`
	OrgId       int    `json:"orgId"`
	FolderId    int    `json:"folderId"`
	Uid         string `json:"uid"`
	Name        string `json:"name"`
	Kind        int    `json:"kind"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	Meta        struct {
		FolderName          string `json:"folderName"`
		FolderUid           string `json:"folderUid"`
		ConnectedDashboards int    `json:"connectedDashboards"`
	} `json:"meta"`
}

type LibraryElementsResponse struct {
	Result struct {
		TotalCount int                      `json:"totalCount"`
		Element    []LibraryElementResponse `json:"elements"`
		Page       int                      `json:"page"`
		PerPage    int                      `json:"perPage"`
	} `json:"result"`
}

type LibraryElementsResponseRaw struct {
	Result struct {
		TotalCount int               `json:"totalCount"`
		Element    []json.RawMessage `json:"elements"`
		Page       int               `json:"page"`
		PerPage    int               `json:"perPage"`
	} `json:"result"`
}

// Library represents a Grafana library (panel), with its JSON definition, slug and
// current version.
type Library struct {
	RawJSON []byte
	Name    string
	Slug    string
	Version int
}

type libraryCreateOrUpdateRequest struct {
	FolderUid string          `json:"folderUid"`
	FolderId  int             `json:"folderId"`
	Name      string          `json:"name"`
	Model     json.RawMessage `json:"model"`
	Kind      int             `json:"kind"`
	UID       string          `json:"uid"`
}

type libraryUpdateRequest struct {
	libraryCreateOrUpdateRequest
	Version int `json:"version"`
}

type LibraryElementRaw struct {
	RawJSON []byte
	Uid     string
}

// setLibraryNameFromRawJSON finds a library's name from the content of its
// RawJSON field
func (d *Library) setLibraryNameFromRawJSON() (err error) {
	// Define the necessary structure to catch the library's name
	var library struct {
		Name string `json:"title"`
	}

	// Unmarshal the JSON content into the structure and set the library's
	// name
	err = json.Unmarshal(d.RawJSON, &library)
	d.Name = library.Name

	return
}

// GetLibraryList requests the Grafana API for all library definitions.
// Returns the []library as an instance of the library structure.
// Returns an error if there was an issue requesting the library or parsing
// the response body.
func (c *Client) GetLibraryList() (lib []LibraryElementResponse, raw []json.RawMessage, err error) {
	body, err := c.request("GET", "library-elements/", nil)
	if err != nil {
		return
	}
	resp := new(LibraryElementsResponse)
	err = json.Unmarshal(body, resp)
	if err != nil {
		return
	}
	respRaw := new(LibraryElementsResponseRaw)
	err = json.Unmarshal(body, respRaw)

	lib = resp.Result.Element
	raw = respRaw.Result.Element
	return
}

// GetLibrary requests the Grafana API for a library identified by a given
// URI (using the same format as GetlibrarysURIs).
// Returns the library as an instance of the library structure.
// Returns an error if there was an issue requesting the library or parsing
// the response body.
func (c *Client) GetLibrary(URI string) (lib *Library, err error) {
	body, err := c.request("GET", "library-elements/"+URI, nil)
	if err != nil {
		return
	}

	lib = new(Library)
	err = json.Unmarshal(body, lib)
	return
}

// CreateOrUpdateLibrary takes a given JSON content (as []byte) and create the
// library if it doesn't exist on the Grafana instance, else updates the
// existing one.
// Returns an error if there was an issue generating the request body, performing
// the request or decoding the response's body.
func (c *Client) CreateOrUpdateLibrary(contentJSON []byte, folderUid string, libVersion int) (err error) {
	contentJSONstr := string(contentJSON)
	contentJSONstr, err = sjson.Set(contentJSONstr, "model.libraryPanel.version", libVersion)
	contentJSONstr, _ = sjson.Delete(contentJSONstr, "model.libraryPanel.created")
	contentJSONstr, _ = sjson.Delete(contentJSONstr, "model.libraryPanel.createdBy")
	contentJSONstr, _ = sjson.Delete(contentJSONstr, "model.libraryPanel.updated")
	contentJSONstr, _ = sjson.Delete(contentJSONstr, "model.libraryPanel.updatedBy")

	contentJSONstr, _ = sjson.Delete(contentJSONstr, "meta.created")
	contentJSONstr, _ = sjson.Delete(contentJSONstr, "meta.updated")

	reqBody := libraryCreateOrUpdateRequest{}
	err = json.Unmarshal([]byte(contentJSONstr), &reqBody)
	if err != nil {
		return
	}
	reqBody.FolderUid = folderUid
	// grafana 8.5 doesn't understand folderUIDs, only folderIDs. Look it up.
	folders, err := c.GetFolderList()
	if err != nil {
		return
	}
	for _, folder := range folders {
		if folder.Uid == folderUid {
			reqBody.FolderId = folder.Id
			logrus.Infof("Found folder ID %v for UID %v (%v)", folder.Id, folder.Uid, folder.Title)
			break
		}
	}

	var reqUpdateBody = new(libraryUpdateRequest)
	reqUpdateBody.libraryCreateOrUpdateRequest = reqBody

	var v interface{}
	if err := json.Unmarshal(reqBody.Model, &v); err != nil {
		logrus.Errorf("Failed to unmarshall model: %v", err.Error())
	}
	// the version we are trying to update must match the version stored in grafana already
	reqUpdateBody.Version = libVersion
	//	reqUpdateBody.Version = int(version)
	reqUpdateBodyJSON, err := json.Marshal(reqUpdateBody)

	// Generate the request body's JSON
	reqBodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return
	}
	err = c.createOrUpdateLibraryFolder(reqBodyJSON, reqUpdateBodyJSON, contentJSON, "library-elements", reqBody.UID)
	return
}

func (c *Client) createOrUpdateLibraryFolder(reqBodyJSON []byte, reqUpdateBodyJSON []byte, contentJSON []byte, apiPath string, UID string) (err error) {
	// try "create" first, if it already exists then create will return 400
	err = c.createOrUpdateLibraryFolderMethod(reqBodyJSON, apiPath, "POST")
	if err != nil {
		httpError, isHttpUnknownError := err.(*httpUnknownError)
		if isHttpUnknownError {
			if httpError.StatusCode == 400 { // can't update a library with a POST, try a PATCH to the UID
				logrus.Infof("%v. %v", string(reqUpdateBodyJSON), err.Error())
				err = c.createOrUpdateLibraryFolderMethod(reqUpdateBodyJSON, apiPath+"/"+UID, "PATCH")
				if err != nil {
					logrus.Warnf("Patch failed, %v", err.Error())
				}
				return
			}
		}
	}
	return
}

func (c *Client) createOrUpdateLibraryFolderMethod(reqBodyJSON []byte, apiPath string, method string) (err error) {
	// Send the request
	respBodyJSON, err := c.request(method, apiPath, reqBodyJSON)
	if err != nil {
		logrus.Warnf("Failed to create/update library method (%v) %v %v", method, apiPath, string(respBodyJSON))
		return
	}
	// Decode the response body
	var respBody LibraryElementResponse
	if err = json.Unmarshal(respBodyJSON, &respBody); err != nil {
		return
	}
	return
}

type FolderResponse struct {
	Id    int    `json:"id"`
	Uid   string `json:"uid"`
	Title string `json:"title"`
}
type FoldersResponse []FolderResponse

// GetFolderList requests the Grafana API for all folder definitions.
func (c *Client) GetFolderList() (folders FoldersResponse, err error) {
	body, err := c.request("GET", "folders", nil)
	if err != nil {
		return
	}
	var f FoldersResponse
	err = json.Unmarshal(body, &f)
	logrus.Infof("Got a body of %v %+v", string(body), f)
	folders = f

	if err != nil {
		return
	}
	return
}

// DeleteLibrary deletes the library identified by a given UID.
func (c *Client) DeleteLibrary(uid string) (err error) {
	_, err = c.request("DELETE", "library-elements/"+uid, nil)
	return
}
