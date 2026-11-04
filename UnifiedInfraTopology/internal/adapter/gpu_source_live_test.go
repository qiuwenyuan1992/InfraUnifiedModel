package adapter

import (
 "context"
 "os"
 "testing"
 "time"
 "UnifiedInfraTopology/internal/model"
 "github.com/spf13/viper"
 "github.com/stretchr/testify/require"
)

func TestLiveIDC238GPUDiscoveryReadOnly(t *testing.T){
 path:=os.Getenv("GPU_DISCOVERY_READ_CONFIG");if path==""{t.Skip("显式配置后只读探测IDC238 GPU服务器范围")}
 conf:=viper.New();conf.SetConfigFile(path);require.NoError(t,conf.ReadInConfig());c:=NewCMDB(conf)
 cfg,err:=c.configFor(model.Source{ConfigRef:"idc238"});require.NoError(t,err);cfg.DeviceCondition=map[string]any{"idc_id":238,"parent_type_id":52,"device_type_id":58};cfg.PageSize=20
 ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
 page,err:=c.collectDeviceViewMapPage(ctx,cfg,0,[]string{"inst_id","device_sn","parent_type_id","device_type_id"});require.NoError(t,err)
 t.Logf("IDC238 GPU servers=%d first_page=%d",*page.Count,len(*page.Info))
 for index,item:=range *page.Info{if index==5{break};t.Logf("GPU server sn=%s parent=%s type=%s",item["device_sn"],item["parent_type_id"],item["device_type_id"])}
}
