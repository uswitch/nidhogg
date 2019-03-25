package nidhogg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"unicode"

	"github.com/ghodss/yaml"
)

// ToJSON converts a single YAML document into a JSON document
// or returns an error. If the document appears to be JSON the
// YAML decoding path is not used.
func toJSON(data []byte) ([]byte, error) {
	if hasJSONPrefix(data) {
		return data, nil
	}
	return yaml.YAMLToJSON(data)
}

// ...

var jsonPrefix = []byte("{")

// hasJSONPrefix returns true if the provided buffer appears to start with
// a JSON open brace.
func hasJSONPrefix(buf []byte) bool {
	return hasPrefix(buf, jsonPrefix)
}

// Return true if the first non-whitespace bytes in buf is prefix.
func hasPrefix(buf []byte, prefix []byte) bool {
	trim := bytes.TrimLeftFunc(buf, unicode.IsSpace)
	return bytes.HasPrefix(trim, prefix)
}

//GetConfig reads the config file, parses it whether it be in json or yaml and returns a handler config
func GetConfig(config string) (HandlerConfig, error) {

	var handlerConf HandlerConfig
	bytes, err := ioutil.ReadFile(config)
	if err != nil {
		return HandlerConfig{}, fmt.Errorf("unable to read config file: %v", err)
	}

	bytes, err = toJSON(bytes)
	if err != nil {
		return HandlerConfig{}, fmt.Errorf("error parsing file: %v", err)
	}

	err = json.Unmarshal(bytes, &handlerConf)
	if err != nil {
		return HandlerConfig{}, fmt.Errorf("error parsing file: %v", err)
	}

	return handlerConf, nil

}
