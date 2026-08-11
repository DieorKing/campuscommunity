package conf

import (
	"fmt"

	"github.com/spf13/viper"
)

func Init(filename string) (err error) {
	//设置配置信息路径
	viper.SetConfigFile(filename)
	//读取配置信息
	if err = viper.ReadInConfig(); err != nil {
		fmt.Printf("viper ReadInConfig failed:%v\n", err)
		return
	}
	//反序列化到对象中
	if err = viper.Unmarshal(Conf); err != nil {
		fmt.Printf("viper.Unmarshal failed err:%v\n", err)
		return
	}
	return
}
