package utils

import (
	"os/exec"
	"regexp"
	"strings"
)

var macRegex = regexp.MustCompile(`([0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}`)

func LookupMAC(ip string) string {
	if ip == "" || strings.HasPrefix(ip, "127.") {
		return ""
	}

	output, err := exec.Command("arp", "-a", ip).Output()
	if err != nil {
		return ""
	}

	matches := macRegex.FindAllString(string(output), -1)
	for _, match := range matches {
		return strings.ToUpper(strings.ReplaceAll(match, "-", ":"))
	}

	return ""
}
