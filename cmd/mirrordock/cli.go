package main

import (
	"flag"
	"fmt"
	"io"
)

type cliOptions struct {
	ConfigPath    string
	Help, Version bool
}

const usageText = `mirrordock — 源仓
用法：mirrordock [-c config.yaml | --config config.yaml]
  -c, --config  YAML 配置文件（默认 config.yaml）
  -h, --help    显示帮助
      --version 显示版本
管理页面位于 /admin/；同步与服务设置使用 YAML 配置。
`

func parseArgs(args []string) (*cliOptions, error) {
	options := &cliOptions{ConfigPath: "config.yaml"}
	fs := flag.NewFlagSet("mirrordock", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var short, long string
	fs.StringVar(&short, "c", "", "配置文件路径")
	fs.StringVar(&long, "config", "", "配置文件路径")
	fs.BoolVar(&options.Help, "h", false, "显示帮助")
	fs.BoolVar(&options.Help, "help", false, "显示帮助")
	fs.BoolVar(&options.Version, "version", false, "显示版本")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	invalidEmpty := false
	fs.Visit(func(f *flag.Flag) {
		if (f.Name == "c" || f.Name == "config") && f.Value.String() == "" {
			invalidEmpty = true
		}
	})
	if invalidEmpty {
		return nil, fmt.Errorf("配置文件路径不能留空")
	}
	if short != "" {
		options.ConfigPath = short
	}
	if long != "" {
		if short != "" && short != long {
			return nil, fmt.Errorf("-c 和 --config 不一致")
		}
		options.ConfigPath = long
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("不接受额外位置参数")
	}
	return options, nil
}
