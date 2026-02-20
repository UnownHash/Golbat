package decoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const masterFileURL = "https://raw.githubusercontent.com/WatWowMap/Masterfile-Generator/master/master-latest-basics.json"

var masterFileCachePath = "cache/master-latest-basics.json"
var masterFileHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

var (
	errMasterFileFetch      = errors.New("can't fetch remote MasterFile")
	errMasterFileOpen       = errors.New("can't open MasterFile")
	errMasterFileUnmarshall = errors.New("can't unmarshall MasterFile")
	errMasterFileMarshall   = errors.New("can't marshall MasterFile")
	errMasterFileSave       = errors.New("can't save MasterFile")
)

type MasterFileData struct {
	Initialized bool                      `json:"-"`
	Pokemon     map[int]MasterFilePokemon `json:"pokemon"`
	Costumes    map[int]bool              `json:"costumes"`
}

type MasterFilePokemon struct {
	Name  string                 `json:"name"`
	Types []int                  `json:"types"`
	Forms map[int]MasterFileForm `json:"forms"`
}

type MasterFileForm struct {
	Types []int `json:"types"`
}

type rawMasterFile struct {
	Pokemon  map[string]rawMasterFilePokemon `json:"pokemon"`
	Costumes map[string]json.RawMessage      `json:"costumes"`
}

type rawMasterFilePokemon struct {
	Name  string                              `json:"name,omitempty"`
	Types []int                               `json:"types"`
	Forms map[string]rawMasterFilePokemonForm `json:"forms"`
}

type rawMasterFilePokemonForm struct {
	Types []int `json:"types"`
}

var (
	watcherChan    chan bool
	masterFileMu   sync.RWMutex
	masterFileRaw  []byte
	masterFileData MasterFileData
)

func EnsureMasterFileData() error {
	if err := FetchMasterFileData(); err != nil {
		log.Warnf("MasterFile fetch failed: %v", err)
		if err2 := LoadMasterFileData(""); err2 != nil {
			log.Warnf("Loading MasterFile from cache failed: %v", err2)
			if err3 := LoadMasterFileData("pogo/master-latest-basics.json"); err3 != nil {
				return fmt.Errorf("masterfile unavailable (fetch: %w, cache: %v, fallback: %v)", err, err2, err3)
			}
			log.Warnf("Loaded MasterFile from bundled fallback")
		} else {
			log.Warnf("Loaded MasterFile from cache")
		}
	} else {
		log.Infof("MasterFile fetched successfully")
		if err := SaveMasterFileData(); err != nil {
			log.Warnf("Storing MasterFile cache under %s has failed: %v", masterFileCachePath, err)
		}
	}
	return nil
}

// FetchMasterFileData downloads and loads the remote masterfile.
func FetchMasterFileData() error {
	data, err := downloadMasterFile()
	if err != nil {
		return err
	}
	return loadMasterFileBytes(data)
}

// LoadMasterFileData loads the masterfile from disk.
func LoadMasterFileData(filePath string) error {
	if filePath == "" {
		filePath = masterFileCachePath
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return errMasterFileOpen
	}
	return loadMasterFileBytes(data)
}

// SaveMasterFileData writes the raw masterfile to cache.
func SaveMasterFileData() error {
	masterFileMu.RLock()
	if len(masterFileRaw) == 0 {
		masterFileMu.RUnlock()
		return errMasterFileMarshall
	}
	raw := make([]byte, len(masterFileRaw))
	copy(raw, masterFileRaw)
	masterFileMu.RUnlock()

	if err := os.WriteFile(masterFileCachePath, raw, 0644); err != nil {
		return errMasterFileSave
	}
	return nil
}

func WatchMasterFileData() error {
	if watcherChan != nil {
		return errors.New("MasterFile watcher is already running")
	}

	log.Infof("MasterFile watcher started")
	watcherChan = make(chan bool)
	interval := 60 * time.Minute

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-watcherChan:
				log.Infof("MasterFile watcher stopped")
				return
			case <-ticker.C:
				log.Infof("Checking remote MasterFile")
				data, err := downloadMasterFile()
				if err != nil {
					log.Infof("Remote MasterFile fetch failed: %v", err)
					continue
				}
				masterFileMu.RLock()
				same := bytes.Equal(masterFileRaw, data)
				masterFileMu.RUnlock()
				if same {
					continue
				}
				if err := loadMasterFileBytes(data); err != nil {
					log.Warnf("Unable to parse new MasterFile: %v", err)
					continue
				}
				if err := SaveMasterFileData(); err != nil {
					log.Warnf("Storing MasterFile cache under %s has failed: %v", masterFileCachePath, err)
				} else {
					log.Infof("MasterFile cache saved to %s", masterFileCachePath)
					reloadOhbemFromMasterFile()
				}
			}
		}
	}()
	return nil
}

func downloadMasterFile() ([]byte, error) {
	req, err := http.NewRequest("GET", masterFileURL, nil)
	if err != nil {
		return nil, errMasterFileFetch
	}
	req.Header.Set("User-Agent", "Golbat")

	resp, err := masterFileHTTPClient.Do(req)
	if err != nil {
		return nil, errMasterFileFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errMasterFileFetch
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errMasterFileFetch
	}
	return body, nil
}

func loadMasterFileBytes(data []byte) error {
	var raw rawMasterFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return errMasterFileUnmarshall
	}

	parsed := MasterFileData{
		Pokemon:  make(map[int]MasterFilePokemon, len(raw.Pokemon)),
		Costumes: make(map[int]bool, len(raw.Costumes)),
	}

	for pid, pokemon := range raw.Pokemon {
		intPid, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		forms := make(map[int]MasterFileForm, len(pokemon.Forms))
		for fid, form := range pokemon.Forms {
			intFid, err := strconv.Atoi(fid)
			if err != nil {
				continue
			}
			forms[intFid] = MasterFileForm{
				Types: append([]int(nil), form.Types...),
			}
		}
		parsed.Pokemon[intPid] = MasterFilePokemon{
			Name:  pokemon.Name,
			Types: append([]int(nil), pokemon.Types...),
			Forms: forms,
		}
	}

	for cid := range raw.Costumes {
		if intCid, err := strconv.Atoi(cid); err == nil {
			parsed.Costumes[intCid] = true
		}
	}

	parsed.Initialized = true

	masterFileMu.Lock()
	masterFileData = parsed

	masterFileRaw = make([]byte, len(data))
	copy(masterFileRaw, data)
	masterFileMu.Unlock()
	return nil
}

func getMasterFileData() MasterFileData {
	masterFileMu.RLock()
	data := masterFileData
	masterFileMu.RUnlock()
	return data
}
