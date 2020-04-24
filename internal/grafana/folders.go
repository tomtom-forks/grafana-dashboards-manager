package grafana

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
)

// folderCreateOrUpdateRequest represents the request sent to create or update a
// folder
type folderCreateOrUpdateRequest struct {
	Uid       string `json:"uid"`
	Title     string `json:"title"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

func (c *Client) CreateFolders(folders []string, contents map[string][]byte) (err error) {
	logrus.Info("Create folders")

	for _, folderName := range folders {
		var folder Folder
		err = json.Unmarshal(contents[folderName], &folder)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"contents": string(contents[folderName]),
			}).Info("Unable to unmarshall folder")
		}
		logrus.WithFields(logrus.Fields{
			"title": folder.Title,
			//	"contents": contents,
			"UID": folder.UID,
		}).Info("Create folders")
		err = c.CreateOrUpdateFolder(folder.Title, folder.UID)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
			}).Info("Unable to create folder")
		}
	}
	return
}

// CreateOrUpdateFolder takes a given JSON content (as []byte) and create the
// dashboard if it doesn't exist on the Grafana instance, else updates the
// existing one. The Grafana API decides whether to create or update based on the
// "id" attribute in the dashboard's JSON: If it's unkown or null, it's a
// creation, else it's an update.
// Returns an error if there was an issue generating the request body, performing
// the request or decoding the response's body.
func (c *Client) CreateOrUpdateFolder(title string, uid string) (err error) {
	reqBody := folderCreateOrUpdateRequest{
		Title:     title,
		Uid:       uid,
		Overwrite: true,
	}
	// Generate the request body's JSON
	reqBodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return
	}
	err = c.createOrUpdateDashboardFolder(reqBodyJSON, reqBodyJSON, "folders")
	if err != nil {
		logrus.Info("Failed to recreate dashboard - trying again")

		err = c.createOrUpdateDashboardFolderMethod(reqBodyJSON, reqBodyJSON, "folders/"+uid, "PUT")
	}
	return
}

// DeleteFolder deletes the dashboard identified by a given uid on the
// Grafana API. NB this also deletes all graphs stored inside!
// Returns an error if the process failed.
func (c *Client) DeleteFolder(uid string) (err error) {
	_, err = c.request("DELETE", "dashboards/db/"+uid, nil)
	return
}
