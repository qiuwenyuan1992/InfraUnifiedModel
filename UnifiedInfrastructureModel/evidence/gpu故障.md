请求地址： http://pre.inframonitor.jd.com/api/topology_grid/gpu/fault_location/query

请求方法：POST

请求参数：

keywords 查询的单个IP数组或IP对
timeRange 时间窗口  
level  严重度(critical  warning all) 
status 状态(active  resolved  all)

```json 
{
    "timeRange": [
        "2026-07-02 14:28:29",
        "2026-07-02 15:28:29"
    ],
    "keywords": [
        "6.1.60.71",
        "6.1.60.61"
    ],
    "level": "all",
    "status": "active"
}
```

返回值： 

```json
{
    "code": 200,
    "data": [
        {
            "resourceType": "IP",
            "queryContent": "6.1.60.71",
            "dataCenter": "大同灵丘M3",
            "opsGroup": "TPAAS_NN_JDOS",
            "podName": "POD010",
            "serverIp": "6.1.60.71",
            "status": "异常",
            "result": "",
            "faults": [
                {
                    "level": "",
                    "status": "活跃中",
                    "startTime": "2026-07-02 15:07:22",
                    "endTime": "",
                    "name": "风扇故障",
                    "description": "ipmi: Health EventLog2026-07-02T15:07:21.601567+08:00|6.166.0.46|OpenBMC.0.1.Critical| [IPMI-2002] MB FAN3, Lower Critical - going low - Assertion|Fan(MB FAN3)|4|1|98|52|0|3",
                    "portName": "",
                    "ip": "6.1.60.71",
                    "sn": "A946907X5610069",
                    "uuid": "6ca3af511a7aaca9f3fda15d9ad2987a",
                    "from": "server_fault_pool"
                }
            ],
            "linkFaults": []
        },
        {
            "resourceType": "IP",
            "queryContent": "6.1.60.61",
            "dataCenter": "大同灵丘M3",
            "opsGroup": "TPAAS_NN_JDOS",
            "podName": "POD010",
            "serverIp": "6.1.60.61",
            "status": "异常",
            "result": "",
            "faults": [
                {
                    "level": "",
                    "status": "活跃中",
                    "startTime": "2026-07-02 14:56:05",
                    "endTime": "",
                    "name": "电源故障",
                    "description": "powerSupplyFailure CRITICAL \"Status Events\" 6.166.0.40 - Power Supply Event: Power Supply failure detected",
                    "portName": "",
                    "ip": "6.1.60.61",
                    "sn": "A946907X5610858",
                    "uuid": "4eb4e2af2c94c13d73bc9c8118dac87e",
                    "from": "server_fault_pool"
                },
                {
                    "level": "",
                    "status": "活跃中",
                    "startTime": "2026-07-02 14:56:03",
                    "endTime": "",
                    "name": "电源故障",
                    "description": "powerSupplyFailure Normal \"Status Events\" 6.166.0.40 - Power supply failure event - Asserted. Hex-STRING: 42 36 30 31 4D 53 90 5A 08 16 C0 CD 00 00 00 00  01 35 9B 9F 81 FF FF 20 20 10 FF C5 00 00 01  FF 00 00 00 00 00 FF 00 00 2A 7C 1D 51 80 10  08 50 53 36 20 53 74 61 74 75 73 00 00 00 00  00 C1  PWR-0001 PS6 Status, Power Supply Failure detected",
                    "portName": "",
                    "ip": "6.1.60.61",
                    "sn": "A946907X5610858",
                    "uuid": "5011e8ce0617d2951d77b429a08a08d6",
                    "from": "server_fault_pool"
                }
            ],
            "linkFaults": []
        }
    ],
    "message": "OK"
}
```