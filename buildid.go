package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
)

func genBuildID() string {
	buf := make([]byte, 18)
	rand.Read(buf)
	str := base64.RawURLEncoding.EncodeToString(buf)
	str = strings.ReplaceAll(str, "-", "a")
	str = strings.ReplaceAll(str, "_", "b")
	return BuildIDPrefix + str
}

func validBuildID(id string) error {
	if ok, err := regexp.MatchString("^"+BuildIDPrefix+"[a-zA-Z0-9]{16,64}$", id); err != nil || !ok {
		return errors.New("bad build id")
	}
	return nil
}
