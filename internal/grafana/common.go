package grafana

import (
	"encoding/json"
	"github.com/bruce34/grafana-dashboards-manager/internal/config"
	"github.com/bruce34/grafana-dashboards-manager/internal/grafana/helpers"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// FilterIgnored takes a map mapping files' names to their contents and remove
// all the files that are supposed to be ignored by the dashboard manager.
// An ignored file is either named "versions.json" or describing a dashboard
// which slug starts with a given prefix.
// Returns an error if the slug couldn't be tested against the prefix.
func FilterIgnored(
	filesToPush *map[string][]byte, cfg *config.Config,
) (err error) {
	for filename, content := range *filesToPush {
		max := len(content)
		if max > 20 {
			max = 20
		}
		logrus.WithFields(logrus.Fields{
			"filename":    filename,
			"content":	string(content[:max]),
		}).Info("Checking whether to ignore")
		// Don't set versions.json to be pushed
		if strings.HasSuffix(filename, "versions-metadata.json") {
			delete(*filesToPush, filename)
			continue
		}

		// Check if dashboard is ignored
		ignored, err := isIgnored(content, cfg)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"filename": filename,
				"err":      err,
				"content":  string(content),
			}).Info("Ignoring because of error")
			ignored = true
		}

		if ignored {
			delete(*filesToPush, filename)
		}
	}
	return
}

func FolderIDFromFolderUID(versionsFile VersionFile, folderUID string) (folderID int) {
	logrus.WithFields(logrus.Fields{
		"folderUID": folderUID,
	}).Debug("Checking folders by meta ID")
	for _, f := range versionsFile.FoldersMetaByUID {
		logrus.WithFields(logrus.Fields{
			"ID": f.ID,
			"folderUID": f.UID,
		}).Debug("Checking ")
		if folderUID == f.UID {
			logrus.WithFields(logrus.Fields{
				"ID": f.ID,
				"folderUID": f.UID,
			}).Debug("Found")
			folderID = f.ID
		}
	}
	if folderUID != "" {
		logrus.WithFields(logrus.Fields{
			"folderUID": folderUID,
		}).Warn("Failed to find folderUID")
	}
	return
}
// PushFiles takes a slice of files' names and a map mapping a file's name to its
// content, and iterates over the first slice. For each file name, it will push
// to Grafana the content from the map that matches the name, as a creation or
// an update of an existing dashboard.
// Logs any errors encountered during an iteration, but doesn't return until all
// creation and/or update requests have been performed.
func PushFiles(filenames []string, contents map[string][]byte, versionsFile VersionFile, grafanaVersionFile VersionFile, client *Client) {
	// Push all files to the Grafana API
	for _, filename := range filenames {
		_, err := helpers.GetSlug(contents[filename])
		folderID := 0
		if _, ok := contents[filename]; !ok {
			continue
		}

		if err == nil {
			var fld struct {
				FolderUID string `json:"__folderUID"`
			}
			err = json.Unmarshal(contents[filename], &fld)
			folderUID := fld.FolderUID
			logrus.WithFields(logrus.Fields{
				"folderUID":    folderUID,
				"filename": filename,
			}).Info("Grafana: Create/Upload folderUID")

			folderID = FolderIDFromFolderUID(grafanaVersionFile, folderUID)
		} else {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to find title")
		}
		logrus.WithFields(logrus.Fields{
			"folderID":    folderID,
			"filename": filename,
		}).Info("Grafana: Create/Upload folderID")
		if err := client.CreateOrUpdateDashboard(contents[filename], folderID); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to push the file to Grafana")
		}
	}
}

// PushFolders takes a slice of files' names and a map mapping a file's name to its
// content, and iterates over the first slice. For each file name, it will push
// to Grafana the content from the map that matches the name, as a creation or
// an update of an existing dashboard.
// Logs any errors encountered during an iteration, but doesn't return until all
// creation and/or update requests have been performed.
func PushFolders(filenames []string, contents map[string][]byte, versionsFile VersionFile, grafanaVersionFile VersionFile, client *Client) (err error) {
	// Push all files to the Grafana API
	for _, filename := range filenames {
		var folder Folder
		if _, ok := contents[filename]; !ok {
			continue
		}
		if err = json.Unmarshal(contents[filename], &folder); err != nil {
			return
		}

		if err = client.CreateOrUpdateFolder(folder.Title, folder.UID); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to push the file to Grafana")
		}
	}
	return
}

// DeleteDashboards takes a slice of files' names and a map mapping a file's name
// to its content, and iterates over the first slice. For each file name, extract
// a dashboard's slug from the content, in the map, that matches the name, and
// will use it to send a deletion request to the Grafana API.
// Logs any errors encountered during an iteration, but doesn't return until all
// deletion requests have been performed.
func DeleteDashboards(filenames []string, contents map[string][]byte, client *Client) {
	for _, filename := range filenames {
		// Retrieve dashboard slug because we need it in the deletion request.
		slug, err := helpers.GetSlug(contents[filename])
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
			}).Error("Failed to compute the dashboard's slug")
		}

		if err := client.DeleteDashboard(slug); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":    err,
				"filename": filename,
				"slug":     slug,
			}).Error("Failed to remove the dashboard from Grafana")
		}
	}
}

// isIgnored checks whether the file must be ignored, by checking if there's an
// prefix for ignored files set in the configuration file, and if the dashboard
// described in the file has a name that starts with this prefix. Returns an
// error if there was an issue reading or decoding the file.
func isIgnored(dashboardJSON []byte, cfg *config.Config) (bool, error) {
	// If there's no prefix set, no file is ignored
	if len(cfg.Grafana.IgnorePrefix) == 0 {
		return false, nil
	}

	// Parse the file's content to extract its slug
	slug, err := helpers.GetSlug(dashboardJSON)
	if err != nil {
		return false, err
	}

	// Compare the slug against the prefix
	if strings.HasPrefix(slug, cfg.Grafana.IgnorePrefix) {
		return true, nil
	}

	return false, nil
}

func Push(cfg *config.Config, fileVersionFile VersionFile, grafanaVersionFile VersionFile, files []string, contents map[string][]byte, client *Client) (err error){
	// Filter out all files that are supposed to be ignored by the
	// dashboard manager.
	if err = FilterIgnored(&contents, cfg); err != nil {
		return err
	}

	// Push the contents of the files that were added or modified to the
	// Grafana API.
	PushFiles(files, contents, fileVersionFile, grafanaVersionFile, client)
	return
}


// getFilesContents takes a slice of files' names and a map mapping a file's name
// to its content and appends to it the current content of all of the files for
// which the name appears in the slice.
// Returns an error if there was an issue reading a file.
func GetFilesContents(
	filenames []string, contents *map[string][]byte, subdir string, cfg *config.Config,
) (err error) {
	// Iterate over files' names
	for _, filename := range filenames {
		// Compute the file's path
		filePath := filepath.Join(cfg.Git.ClonePath, subdir, filename)
		// Read the file's content
		fileContent, err := ioutil.ReadFile(filePath)
		if err != nil {
			return err
		}

		// Append the content to the map
		(*contents)[filename] = fileContent
	}
	return
}


func LoadFilesFromDirectory(cfg *config.Config, dir string, subdir string) (filenames []string, contents map[string][]byte, err error){
	filenames = make([]string, 0)
	contents = make(map[string][]byte)
	files, err := ioutil.ReadDir(filepath.Join(dir, subdir))
	if err != nil {
		return
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			filenames = append(filenames, file.Name())
		}
	}
	err = GetFilesContents(filenames, &contents, subdir, cfg)
	return
}
