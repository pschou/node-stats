//
//  Super simple NFTables endpoint for Prometheus
//
//  Written by Paul Schou (github@paulschou.com)
//
package main

import (
	"encoding/json"
	//"flag"
	"fmt"
	//"log"
	//"net/http"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type nftables struct {
	Entry []interface{} `json:"nftables"`
}

var size_suffix = "_SIZE"
var size_bins = []int{}

/*
func main() {
	listen := flag.String("listen", ":9732", "ip and port to listen on")
	bins := flag.String("sizebins", "75,150,225,300,375,450,525,600,675,750,825,900,975,1050,1125,1200,1275,1350,1425,1500,4500,9000,inf", "transmit unit size bins in bytes")
	sizesuffix := flag.String("sizesuffix", "_SIZE", "nftables chain suffix to bin")
	flag.Parse()
	size_suffix = *sizesuffix
	for _, i := range strings.Split(*bins, ",") {
		val, err := strconv.Atoi(i)
		if err == nil {
			size_bins = append(size_bins, val)
		}
	}
	size_bins = append(size_bins, 1e10)

	http.HandleFunc("/metrics", GetNFT)
	http.ListenAndServe(*listen, nil)
}
*/

func (m *Metrics) CollectNFTables() {
	// Open our jsonFile
	//jsonFile, err := os.Open("/root/go/src/nft_prom/ruleset")
	jsonFile, err := exec.Command("/usr/sbin/nft", "-j", "list", "ruleset").Output()
	//if err != nil {
	//	log.Fatal(err)
	//}
	// if we os.Open returns an error then handle it
	if err != nil {
		//fmt.Println(err)
		return
	}

	//fmt.Println("Successfully Opened file")
	// defer the closing of our jsonFile so that we can parse it later on
	//defer jsonFile.Close()

	// read our opened xmlFile as a byte array.
	//byteValue, _ := ioutil.ReadAll(jsonFile)

	// we initialize our nftables array
	var nft nftables

	// we unmarshal our byteArray which contains our
	// jsonFile's content into 'users' which we defined above
	d := json.NewDecoder(strings.NewReader(string(jsonFile)))
	d.UseNumber()
	d.Decode(&nft)
	//d.Unmarshal(byteValue, &nft)

	// we iterate through every user within our users array and
	// print out the user Type, their name, and their facebook url
	// as just an example
	//fmt.Println("data ", nft.Entry[4])
	iChain := make(map[string]int)
	for _, nfentry := range nft.Entry {
		nfentry_s := nfentry.(map[string]interface{})
		if rule, ok := nfentry_s["rule"]; ok {
			var bytes interface{}
			var packets interface{}
			rule_s := rule.(map[string]interface{})
			keys := []string{}
			for k, v := range rule_s {
				if k == "chain" {
					tablechain := fmt.Sprintf("%v:%v", rule_s["table"], v)
					iChain[tablechain]++
					keys = append(keys, fmt.Sprintf("num=\"%d\"", iChain[tablechain]))
					//log.Println("table chain", tablechain)

					if strings.HasSuffix(tablechain, size_suffix) && iChain[tablechain] <= len(size_bins) {
						size := size_bins[iChain[tablechain]-1]
						if size == 1e10 {
							keys = append(keys, fmt.Sprintf("le=\"inf\""))
						} else {
							keys = append(keys, fmt.Sprintf("le=\"%d\"", size))
						}
					}
				}

				if k == "expr" {
					for _, ep := range v.([]interface{}) {
						ep_s := ep.(map[string]interface{})

						if ep_s["match"] != nil {
							match_s := (ep_s["match"]).(map[string]interface{})
							//op := match_s["op"](string)
							//right := match_s["right"](string)
							switch lv := match_s["left"].(type) {
							case string:
								keys = append(keys, fmt.Sprintf("left=%s", strconv.Quote(string(lv))))
							default:
								//lvj, _ := json.Marshal(lv)
								lvj := printMap(lv)
								//lvj := fmt.Sprintf("%v", lv)
								keys = append(keys, fmt.Sprintf("left=%s", strconv.Quote(string(lvj))))
							}

							switch rv := match_s["right"].(type) {
							case string:
								keys = append(keys, fmt.Sprintf("right=%s", strconv.Quote(string(rv))))
							default:
								//rvj, _ := json.Marshal(rv)
								//rvj := fmt.Sprintf("%v", rv)
								rvj := printMap(rv)
								keys = append(keys, fmt.Sprintf("right=%s", strconv.Quote(string(rvj))))
							}

							keys = append(keys, fmt.Sprintf("op=%s", strconv.Quote(string(match_s["op"].(string)))))
						}
						if ep_s["jump"] != nil {
							//fmt.Printf("found jump!!! \n", ep_s["jump"])
							jump_s := (ep_s["jump"]).(map[string]interface{})
							keys = append(keys, fmt.Sprintf("jump=\"%v\"", jump_s["target"]))
						}
						if ep_s["counter"] != nil {
							counter_s := (ep_s["counter"]).(map[string]interface{})
							bytes = counter_s["bytes"]
							packets = counter_s["packets"]
							//fmt.Printf("data %v \n", ep_s["counter"])
						}
					}
				} else {
					keys = append(keys, fmt.Sprintf("%v=\"%v\"", k, v))
				}
			}
			m.PrintType("nftables_rule_bytes_total", "gauge", "nftables rule matched total bytes")
			m.PrintStr(strings.Join(keys, ","), fmt.Sprintf("%v", bytes))

			m.PrintType("nftables_rule_bytes_total", "gauge", "nftables rule matched packets")
			m.PrintStr(strings.Join(keys, ","), fmt.Sprintf("%v", packets))

			//fmt.Fprintf(w, "nftables_rule_bytes_total{%s} %v\n", strings.Join(keys, ","), bytes)
			//fmt.Fprintf(w, "nftables_rule_packets_total{%s} %v\n", strings.Join(keys, ","), packets)
		}
	}
	/*    for i := 0; i < len(users.Users); i++ {
	          fmt.Println("User Type: " + users.Users[i].Type)
	          fmt.Println("User Age: " + strconv.Itoa(users.Users[i].Age))
	          fmt.Println("User Name: " + users.Users[i].Name)
	          fmt.Println("Facebook Url: " + users.Users[i].Social.Facebook)
	      }
	*/

}
func printMap(dat interface{}) string {
	//fmt.Printf("dat = %v  (%v)\n", dat, reflect.TypeOf(dat).Kind())
	dat_s, _ := dat.(map[string]interface{})
	s := []string{}
	for k, v := range dat_s {
		switch reflect.TypeOf(v).Kind() {
		//case reflect.String:
		//	s = fmt.Sprintf("%s %s:%s", s, k, v)
		case reflect.Map:
			s = append(s, fmt.Sprintf("%s[%s]", k, printMap(v)))
		//s = fmt.Sprintf("%s %s:%v", s, k, lv)
		default:
			s = append(s, fmt.Sprintf("%s:%s", k, v))
			//s = fmt.Sprintf("%s %s[%s]", k, printMap(lv))
		}
	}
	sort.Strings(s)
	return strings.Join(s, " ")
}
