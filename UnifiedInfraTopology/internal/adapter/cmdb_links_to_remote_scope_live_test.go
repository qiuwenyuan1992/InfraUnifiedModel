package adapter

import (
 "context"
 "encoding/json"
 "os"
 "testing"
 "time"
 "UnifiedInfraTopology/internal/model"
 "github.com/spf13/viper"
 "github.com/stretchr/testify/require"
)

func TestLiveLinksToRejectedRemoteScope(t *testing.T){
 path:=os.Getenv("LINKS_TO_REMOTE_SCOPE_CONFIG");if path==""{t.Skip("显式配置后只读查询两台缺失对端的实际机房")}
 conf:=viper.New();conf.SetConfigFile(path);require.NoError(t,conf.ReadInConfig())
 c:=NewCMDB(conf);cfg,err:=c.configFor(model.Source{ConfigRef:"idc1030"});require.NoError(t,err)
 ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
 for _,sn:=range []string{"2102113774P0K5000105","2102113774P0K5000117"}{
  cfg.DeviceCondition=map[string]any{"device_sn":sn}
  page,err:=c.collectDeviceViewMapPage(ctx,cfg,0,[]string{"inst_id","device_sn","parent_type_id","idc_id","idc_name"});require.NoError(t,err)
  data,err:=json.Marshal(page);require.NoError(t,err);t.Logf("remote_sn=%s source=%s",sn,data)
 }
}
