package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"

	"github.com/openrdap/rdap"
	"github.com/oschwald/geoip2-golang"
	"github.com/spf13/pflag"
)

var (
	firstTTL = pflag.IntP("first-hop", "f", 1, "set initial hop distance, i.e., time-to-live")
	maxTTL   = pflag.IntP("max-hop", "m", 64, "set maximal hop count (default: 64)")
	port     = pflag.IntP("port", "p", 64, "use destination PORT port (default: 33434)")
	tos      = pflag.IntP("tos", "t", 0, "set type of service (TOS) to NUM")
	waitTime = pflag.IntP("wait", "w", 3, "wait seconds for response (default: 3)")
	useICMP  = pflag.BoolP("icmp", "I", false, "use ICMP ECHO as probe, otherwise use UDP datagrams")
)

var rdapClient = &rdap.Client{}

//go:embed GeoLite2-City.mmdb
var geolite2 []byte
var geoReader *geoip2.Reader

func main() {
	pflag.Parse()

	args := []string{"traceroute"}
	args = append(args, "-p", fmt.Sprintf("%d", *port))

	if firstTTL != nil {
		args = append(args, "-f", fmt.Sprintf("%d", *firstTTL))
	}
	if maxTTL != nil {
		args = append(args, "-m", fmt.Sprintf("%d", *maxTTL))
	}
	if waitTime != nil {
		args = append(args, "-w", fmt.Sprintf("%d", *waitTime))
	}
	if useICMP != nil && *useICMP {
		args = append(args, "-I")
	}
	args = append(args, "-q", "1")

	if supportASNLookup() {
		args = append(args, "-A")
	}

	pflagArgs := pflag.Args()
	if len(args) == 0 {
		pflag.Usage()
		return
	}

	var err error
	geoReader, err = geoip2.FromBytes(geolite2)
	if err != nil {
		panic(err)
	}
	defer geoReader.Close()

	dest := pflagArgs[0]
	args = append(args, dest)

	cmd := exec.Command(args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err, ":", stderr.String())
	}

	if err := cmd.Start(); err != nil {
		log.Fatal(err, ":", stderr.String())
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		handleLine(line)
	}

	if err := cmd.Wait(); err != nil {
		log.Fatal(err, ":", stderr.String())
	}
}

func handleLine(line string) {
	if line == "" || strings.Contains(line, "*") {
		fmt.Println(line)
		return
	}

	// ae-6.a02.tkokhk01.hk.bb.gin.ntt.net (168.143.191.98)  115.577 ms
	//  3  206.72.211.148.any2ix.coresite.com (206.72.211.148)  0.742 ms
	items := strings.Fields(line)
	if len(items) < 3 {
		fmt.Println("")
		return
	}

	ip := items[1]
	if strings.HasPrefix(items[2], "(") && strings.HasSuffix(items[2], ")") {
		ip = items[2][1 : len(items[2])-1]
	}

	nodeIP := net.ParseIP(ip)
	isPrivate := nodeIP.IsPrivate()

	var extra []string

	if !isPrivate {
		var registrant string
		if nw, err := rdapClient.QueryIP(ip); err == nil {
			for _, entity := range nw.Entities {
				if entity.Roles[0] != "registrant" {
					continue
				}

				registrant = entity.VCard.Name()
			}

			if registrant == "" && len(nw.Remarks) > 0 {
				registrant = nw.Remarks[0].Description[0]
			}

			extra = append(extra, "["+registrant+"]")
		}

		{
			var geo []string

			if city, err := geoReader.City(nodeIP); err == nil {
				if city.Country.Names["en"] != "" {
					geo = append(geo, city.Country.Names["en"])
				}
				if len(city.Subdivisions) > 0 {
					geo = append(geo, city.Subdivisions[0].Names["en"])
				}
				if city.City.Names["en"] != "" {
					geo = append(geo, city.City.Names["en"])
				}
			}
			extra = append(extra, "["+strings.Join(geo, ",")+"]")
		}

	} else {
		extra = append(extra, "private")
	}

	fmt.Printf("%s, %s\n", line, strings.Join(extra, ", "))
}

func supportASNLookup() bool {
	data, err := exec.Command("traceroute", "--help").CombinedOutput()
	if err != nil {
		return false
	}

	return strings.Contains(string(data), "--as-path-lookups")
}
