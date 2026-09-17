package main

import (
	"encoding/json"
	"fmt"
	"os"

	"wild-work/internal/auth"
	"wild-work/internal/intl"
)

func main() {
	dir := "auths"
	auths, err := auth.LoadWBIntlDir(dir)
	if err != nil || len(auths) == 0 {
		fmt.Println("no wbintl auth:", err)
		return
	}
	acct := auths[0]
	c := intl.NewWithBase("https://www.workbuddy.ai")
	c.Channel = "wbintl"
	models, err := c.FetchModels(acct)
	fmt.Println("FetchModels err:", err, "count:", len(models))
	hasDS := false
	for _, m := range models {
		if m.ID == "deepseek-v4.1-flash" {
			hasDS = true
		}
	}
	fmt.Println("FetchModels 含 deepseek-v4.1-flash:", hasDS)

	pricing, err := c.FetchModelPricing(acct)
	fmt.Println("FetchModelPricing err:", err, "count:", len(pricing))
	for _, p := range pricing {
		if p.Model == "deepseek-v4.1-flash" {
			b, _ := json.Marshal(p)
			fmt.Println("deepseek pricing:", string(b))
		}
	}
	if acct.AccessToken != "" {
		fmt.Println("(token ok, uid=", acct.UID, ")")
	}
	_ = os.Stdout
}