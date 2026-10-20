package engine

import (
	"testing"
)

func TestParseResults(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		wantLen int
		wantRes []string // 各项期望result
		wantErr bool
	}{
		{
			name:    "标准JSON数组",
			stdout:  `[{"test_name":"UART1 自发自收测试","test_item":"uart1","result":"pass","msg":"ok","raw_log":"l"}]`,
			wantLen: 1,
			wantRes: []string{"pass"},
		},
		{
			name: "前后杂字符剔除",
			stdout: `Script done.
[{"test_name":"ETH0","test_item":"eth0","result":"fail","msg":"timeout","raw_log":"ping loss"}]
DONE`,
			wantLen: 1,
			wantRes: []string{"fail"},
		},
		{
			name:    "单个对象自动包裹",
			stdout:  `{"test_name":"GPIO4","test_item":"gpio4","result":"pass","msg":"ok","raw_log":"l"}`,
			wantLen: 1,
			wantRes: []string{"pass"},
		},
		{
			name:    "尾逗号容错",
			stdout:  `[{"test_name":"A","test_item":"a","result":"pass","msg":"m","raw_log":"l",},]`,
			wantLen: 1,
			wantRes: []string{"pass"},
		},
		{
			name:    "result规范化大小写与同义词",
			stdout:  `[{"test_name":"A","test_item":"a","result":"PASS","msg":"m","raw_log":"l"},{"test_name":"B","test_item":"b","result":"Failed","msg":"m","raw_log":"l"}]`,
			wantLen: 2,
			wantRes: []string{"pass", "fail"},
		},
		{
			name:    "无JSON报错",
			stdout:  `no json here`,
			wantErr: true,
		},
		{
			name:    "空输出报错",
			stdout:  `   `,
			wantErr: true,
		},
		{
			name:    "空数组报错",
			stdout:  `[]`,
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, err := ParseResults(c.stdout)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际返回%v", items)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if len(items) != c.wantLen {
				t.Fatalf("项数=%d, 期望%d", len(items), c.wantLen)
			}
			for i, want := range c.wantRes {
				if items[i].Result != want {
					t.Errorf("第%d项result=%q, 期望%q", i, items[i].Result, want)
				}
			}
		})
	}
}

func TestSSHConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SSHConfig
		wantErr bool
	}{
		{"正常", SSHConfig{Host: "192.168.1.100", Port: "22", User: "root", Pass: "123"}, false},
		{"缺IP", SSHConfig{Port: "22", User: "root"}, true},
		{"端口非数字", SSHConfig{Host: "1.1.1.1", Port: "abc", User: "root"}, true},
		{"端口越界", SSHConfig{Host: "1.1.1.1", Port: "70000", User: "root"}, true},
		{"缺用户名", SSHConfig{Host: "1.1.1.1", Port: "22"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}
