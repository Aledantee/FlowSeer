package integration

import (
	"bytes"
	"encoding/xml"
)

func nativeUserExists(config []byte, name string) (bool, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		return false, nil
	}
	var root struct {
		XMLName xml.Name `xml:"http://cisco.com/ns/yang/Cisco-IOS-XE-native native"`
		Users   []struct {
			Name string `xml:"http://cisco.com/ns/yang/Cisco-IOS-XE-native name"`
		} `xml:"http://cisco.com/ns/yang/Cisco-IOS-XE-native username"`
	}
	if err := xml.Unmarshal(config, &root); err != nil {
		return false, err
	}
	for _, user := range root.Users {
		if user.Name == name {
			return true, nil
		}
	}
	return false, nil
}
