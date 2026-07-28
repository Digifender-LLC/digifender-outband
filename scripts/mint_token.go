//go:build ignore
package main
import (
  "context"; "fmt"; "os"; "time"
  "mipmi/internal/amiweb"
)
func main(){
  ctx,_:=context.WithTimeout(context.Background(),20*time.Second)
  args,cookie,err:=amiweb.FetchLaunchArgs(ctx,"192.168.9.74","root",os.Getenv("MIPMI_BMC_PASS"))
  if err!=nil{panic(err)}
  fmt.Printf("%s\n%s\n", args["kvmtoken"], cookie)
}
