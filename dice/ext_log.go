package dice

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/alexmullins/zip"
	"go.etcd.io/bbolt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

type LogOneItem struct {
	Id        uint64 `json:"id"`
	Nickname  string `json:"nickname"`
	IMUserId  string `json:"IMUserId"`
	Time      int64  `json:"time"`
	Message   string `json:"message"`
	IsDice    bool   `json:"isDice"`
	CommandId uint64 `json:"commandId"`

	UniformId string `json:"uniformId"`
	Channel   string `json:"channel"` // 用于秘密团
}

// {"data":null,"msg":"SEND_MSG_API_ERROR","retcode":100,"status":"failed","wording":"请参考 go-cqhttp 端输出"}

func RegisterBuiltinExtLog(self *Dice) {
	privateCommandListen := map[uint64]int64{}

	privateCommandListenCheck := func() {
		now := time.Now().Unix()
		newMap := map[uint64]int64{}
		for k, v := range privateCommandListen {
			// 30s间隔以上清除
			if now-v < 30 {
				newMap[k] = v
			}
		}
		privateCommandListen = newMap
	}

	helpLog := `.log new (<日志名>) // 新建日志并开始记录，注意new后跟空格！
.log on (<日志名>)  // 开始记录，不写日志名则开启最近一次日志，注意on后跟空格！
.log off // 暂停记录
.log end // 完成记录并发送日志文件
.log halt // 强行关闭当前log，不上传日志
.log list // 查看当前群的日志列表
.log del <日志名> // 删除一份日志`

	self.ExtList = append(self.ExtList, &ExtInfo{
		Name:       "log",
		Version:    "1.0.0",
		Brief:      "跑团辅助扩展，提供日志、染色等功能",
		Author:     "木落",
		AutoActive: true,
		OnLoad: func() {
			os.MkdirAll(filepath.Join(self.BaseConfig.DataDir, "logs"), 0755)
			self.DB.Update(func(tx *bbolt.Tx) error {
				_, err := tx.CreateBucketIfNotExists([]byte("logs"))
				return err
			})
		},
		OnMessageSend: func(ctx *MsgContext, messageType string, userId string, text string, flag string) {
			// 记录骰子发言
			if flag == "skip" {
				return
			}
			privateCommandListenCheck()

			if messageType == "private" && ctx.CommandHideFlag != "" {
				if _, exists := privateCommandListen[ctx.CommandId]; exists {
					session := ctx.Session
					group := session.ServiceAtNew[ctx.CommandHideFlag]

					a := LogOneItem{
						Nickname:  ctx.EndPoint.Nickname,
						IMUserId:  UserIdExtract(ctx.EndPoint.UserId),
						UniformId: ctx.EndPoint.UserId,
						Time:      time.Now().Unix(),
						Message:   text,
						IsDice:    true,
						CommandId: ctx.CommandId,
					}
					LogAppend(ctx, group, &a)
				}
			}
			if IsCurGroupBotOnById(ctx.Session, messageType, userId) {
				session := ctx.Session
				group := session.ServiceAtNew[userId]
				if group.LogOn {
					// <2022-02-15 09:54:14.0> [摸鱼king]: 有的 但我不知道
					if ctx.CommandHideFlag != "" {
						// 记录当前指令和时间
						privateCommandListen[ctx.CommandId] = time.Now().Unix()
					}

					a := LogOneItem{
						Nickname:  ctx.EndPoint.Nickname,
						IMUserId:  UserIdExtract(ctx.EndPoint.UserId),
						UniformId: ctx.EndPoint.UserId,
						Time:      time.Now().Unix(),
						Message:   text,
						IsDice:    true,
						CommandId: ctx.CommandId,
					}
					LogAppend(ctx, group, &a)
				}
			}
		},
		OnMessageReceived: func(ctx *MsgContext, msg *Message) {
			// 处理日志
			if ctx.Group != nil {
				if ctx.Group.LogOn {
					// <2022-02-15 09:54:14.0> [摸鱼king]: 有的 但我不知道
					a := LogOneItem{
						Nickname:  ctx.Player.Name,
						IMUserId:  UserIdExtract(ctx.Player.UserId),
						UniformId: ctx.Player.UserId,
						Time:      msg.Time,
						Message:   msg.Message,
						IsDice:    false,
						CommandId: ctx.CommandId,
					}

					LogAppend(ctx, ctx.Group, &a)
				}
			}
		},
		GetDescText: func(ei *ExtInfo) string {
			return GetExtensionDesc(ei)
		},
		CmdMap: CmdMapCls{
			"log": &CmdItemInfo{
				Name:     "log",
				Help:     helpLog,
				LongHelp: "日志指令:\n" + helpLog,
				Solve: func(ctx *MsgContext, msg *Message, cmdArgs *CmdArgs) CmdExecuteResult {
					if ctx.IsCurGroupBotOn {
						group := ctx.Group
						cmdArgs.ChopPrefixToArgsWith("on", "off", "new", "end", "del", "halt")

						if len(cmdArgs.Args) == 0 {
							onText := "关闭"
							if group.LogOn {
								onText = "开启"
							}
							lines, _ := LogLinesGet(ctx, group, group.LogCurName)
							text := fmt.Sprintf("当前故事: %s\n当前状态: %s\n已记录文本%d条", group.LogCurName, onText, lines)
							ReplyToSender(ctx, msg, text)
							return CmdExecuteResult{Matched: true, Solved: true}
						} else {
							if cmdArgs.IsArgEqual(1, "on") {
								name, _ := cmdArgs.GetArgN(2)
								if name == "" {
									name = group.LogCurName
								}

								if name != "" {
									lines, exists := LogLinesGet(ctx, group, name)

									if exists {
										group.LogOn = true
										group.LogCurName = name

										VarSetValueStr(ctx, "$t记录名称", name)
										VarSetValueInt64(ctx, "$t当前记录条数", int64(lines))
										ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_开启_成功"))
									} else {
										VarSetValueStr(ctx, "$t记录名称", name)
										ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_开启_失败_无此记录"))
									}
								} else {
									ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_开启_失败_尚未新建"))
								}
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "off") {
								if group.LogCurName != "" {
									group.LogOn = false
									lines, _ := LogLinesGet(ctx, group, group.LogCurName)
									VarSetValueStr(ctx, "$t记录名称", group.LogCurName)
									VarSetValueInt64(ctx, "$t当前记录条数", int64(lines))
									ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_关闭_成功"))
								} else {
									ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_关闭_失败"))
								}
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "del", "rm") {
								name, _ := cmdArgs.GetArgN(2)
								if name != "" {
									if name == group.LogCurName {
										ReplyToSender(ctx, msg, "不能删除正在进行的log，请用log new开启新的，或log end结束后再行删除")
									} else {
										ok := LogDelete(ctx, group, name)
										if ok {
											ReplyToSender(ctx, msg, "删除log成功")
										} else {
											ReplyToSender(ctx, msg, "删除log失败，可能是名字不对？")
										}
									}
								} else {
									return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
								}
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "get") {
								if ctx.Group.LogCurName != "" {
									fn, password := LogSendToBackend(ctx, group)
									if fn == "" {
										text := fmt.Sprintf("若线上日志出现问题，可换时间获取，或联系骰主在data/default/logs路径下取出日志\n文件名: 群号_日志名_随机数.zip\n解压缩密钥: %s（密钥中不含ilo0字符）", password)
										ReplyToSenderRaw(ctx, msg, text, "skip")
									} else {
										ReplyToSenderRaw(ctx, msg, fmt.Sprintf("跑团日志已上传服务器，链接如下：\n%s", fn), "skip")
										time.Sleep(time.Duration(0.3 * float64(time.Second)))
										text := fmt.Sprintf("若线上日志出现问题，可换时间获取，或联系骰主在data/default/logs路径下取出日志\n文件名: 群号_日志名_随机数.zip\n解压缩密钥: %s（密钥中不含ilo0字符）", password)
										ReplyToSenderRaw(ctx, msg, text, "skip")
									}
								}
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "end") {
								text := DiceFormatTmpl(ctx, "日志:记录_结束")
								ReplyToSender(ctx, msg, text)
								group.LogOn = false

								time.Sleep(time.Duration(0.3 * float64(time.Second)))
								fn, password := LogSendToBackend(ctx, group)
								if fn == "" {
									ReplyToSenderRaw(ctx, msg, "跑团日志上传失败，可联系骰主在data/default/logs路径下取出\n文件名: 群号_日志名_随机数.zip\n解压缩密钥: "+password+" (密钥中不含ilo0字符)", "skip")
								} else {
									ReplyToSenderRaw(ctx, msg, fmt.Sprintf("跑团日志已上传服务器，链接如下：\n%s", fn), "skip")
									time.Sleep(time.Duration(0.3 * float64(time.Second)))
									text := fmt.Sprintf("若线上日志出现问题，可联系骰主在data/default/logs路径下取出日志\n文件名: 群号_日志名_随机数.zip\n解压缩密钥: %s (密钥中不含ilo0字符)", password)
									ReplyToSenderRaw(ctx, msg, text, "skip")
								}
								group.LogCurName = ""
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "halt") {
								text := DiceFormatTmpl(ctx, "日志:记录_结束")
								ReplyToSender(ctx, msg, text)
								group.LogOn = false
								group.LogCurName = ""
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "list") {
								text := DiceFormatTmpl(ctx, "日志:记录_列出_导入语") + "\n"
								lst, err := LogGetList(ctx, group)
								if err == nil {
									for _, i := range lst {
										text += "- " + i + "\n"
									}
									if len(lst) == 0 {
										text += "暂无记录"
									}
								} else {
									text += "获取记录出错，请联系骰主查看服务日志"
								}
								ReplyToSender(ctx, msg, text)
								return CmdExecuteResult{Matched: true, Solved: true}
							} else if cmdArgs.IsArgEqual(1, "new") {
								name, _ := cmdArgs.GetArgN(2)

								if group.LogCurName != "" && name == "" {
									ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_新建_失败_未结束的记录"))
								} else {
									if name == "" {
										todayTime := time.Now().Format("2006_01_02_15_04_05")
										name = todayTime
									}
									group.LogCurName = name
									group.LogOn = true
									ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "日志:记录_新建"))
								}
								return CmdExecuteResult{Matched: true, Solved: true}
							}
						}
						return CmdExecuteResult{Matched: true, Solved: true, ShowLongHelp: true}
					}

					if ctx.IsPrivate {
						ReplyToSender(ctx, msg, DiceFormatTmpl(ctx, "核心:提示_私聊不可用"))
						return CmdExecuteResult{Matched: true, Solved: true}
					}
					return CmdExecuteResult{Matched: true, Solved: false}
				},
			},
		},
	})
}

func filenameReplace(name string) string {
	re := regexp.MustCompile(`/:\*\?"<>\|\\`)
	return re.ReplaceAllString(name, "")
}

func LogSendToBackend(ctx *MsgContext, group *GroupInfo) (string, string) {
	dirpath := filepath.Join(ctx.Dice.BaseConfig.DataDir, "logs")
	os.MkdirAll(dirpath, 0755)

	lines, err := LogGetAllLines(ctx, group)

	zipPassword := RandStringBytesMaskImprSrcSB(12)
	if err == nil {
		// 本地进行一个zip留档，以防万一
		gid := group.GroupId
		fzip, _ := ioutil.TempFile(dirpath, filenameReplace(gid+"_"+group.LogCurName+".*.zip"))
		writer := zip.NewWriter(fzip)
		defer writer.Close()

		text := ""
		for _, i := range lines {
			timeTxt := time.Unix(i.Time, 0).Format("2006-01-02 15:04:05")
			text += fmt.Sprintf("%s(%d) %s\n%s\n\n", i.Nickname, i.IMUserId, timeTxt, i.Message)
		}

		fileWriter, _ := writer.Encrypt("log.txt", zipPassword)
		fileWriter.Write([]byte(text))
		writer.Close()
	}

	if err == nil {
		// 压缩log，发往后端
		data, err := json.Marshal(map[string]interface{}{
			"items": lines,
		})

		if err == nil {
			var zlibBuffer bytes.Buffer
			w := zlib.NewWriter(&zlibBuffer)
			w.Write(data)
			w.Close()

			return UploadFileToWeizaima(ctx.Dice.Logger, group.LogCurName, ctx.EndPoint.UserId, &zlibBuffer), zipPassword
		}
	}
	return "", zipPassword
}

func LogLinesGet(ctx *MsgContext, group *GroupInfo, name string) (int, bool) {
	var size int
	var exists bool
	ctx.Dice.DB.View(func(tx *bbolt.Tx) error {
		// Retrieve the users bucket.
		// This should be created when the DB is first opened.
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(group.GroupId))
		if b1 == nil {
			return nil
		}
		b := b1.Bucket([]byte(name))
		if b == nil {
			return nil
		}
		exists = true
		size = b.Stats().KeyN
		return nil
	})
	return size, exists
}

func LogDelete(ctx *MsgContext, group *GroupInfo, name string) bool {
	var exists bool
	ctx.Dice.DB.Update(func(tx *bbolt.Tx) error {
		// Retrieve the users bucket.
		// This should be created when the DB is first opened.
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(group.GroupId))
		if b1 == nil {
			return nil
		}

		err := b1.DeleteBucket([]byte(name))
		if err != nil {
			return err
		}
		exists = true

		err = b1.Delete([]byte(name))
		if err != nil {
			return err
		}
		return nil
	})
	return exists
}

func LogDeleteLegacy(ctx *MsgContext, group *ServiceAtItem, name string) bool {
	var exists bool
	ctx.Dice.DB.Update(func(tx *bbolt.Tx) error {
		// Retrieve the users bucket.
		// This should be created when the DB is first opened.
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(strconv.FormatInt(group.GroupId, 10)))
		if b1 == nil {
			return nil
		}

		err := b1.DeleteBucket([]byte(name))
		if err != nil {
			return err
		}
		exists = true

		err = b1.Delete([]byte(name))
		if err != nil {
			return err
		}
		return nil
	})
	return exists
}

// LogGetList 获取列表
func LogGetList(ctx *MsgContext, group *GroupInfo) ([]string, error) {
	ret := []string{}
	return ret, ctx.Dice.DB.View(func(tx *bbolt.Tx) error {
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(group.GroupId))
		if b1 == nil {
			return nil
		}

		return b1.ForEach(func(k, v []byte) error {
			ret = append(ret, string(k))
			return nil
		})
	})
}

// LogGetList 获取列表
func LogGetListLegacy(ctx *MsgContext, group *ServiceAtItem) ([]string, error) {
	ret := []string{}
	return ret, ctx.Dice.DB.View(func(tx *bbolt.Tx) error {
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(strconv.FormatInt(group.GroupId, 10)))
		if b1 == nil {
			return nil
		}

		return b1.ForEach(func(k, v []byte) error {
			ret = append(ret, string(k))
			return nil
		})
	})
}

func LogGetAllLines(ctx *MsgContext, group *GroupInfo) ([]*LogOneItem, error) {
	ret := []*LogOneItem{}
	return ret, ctx.Dice.DB.View(func(tx *bbolt.Tx) error {
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(group.GroupId))
		if b1 == nil {
			return nil
		}

		b := b1.Bucket([]byte(group.LogCurName))
		if b == nil {
			return nil
		}

		return b.ForEach(func(k, v []byte) error {
			logItem := LogOneItem{}
			err := json.Unmarshal(v, &logItem)
			if err == nil {
				ret = append(ret, &logItem)
			}

			return nil
		})
	})
}

func LogGetAllLinesLegacy(ctx *MsgContext, group *ServiceAtItem) ([]*LogOneItem, error) {
	ret := []*LogOneItem{}
	return ret, ctx.Dice.DB.View(func(tx *bbolt.Tx) error {
		b0 := tx.Bucket([]byte("logs"))
		if b0 == nil {
			return nil
		}
		b1 := b0.Bucket([]byte(strconv.FormatInt(group.GroupId, 10)))
		if b1 == nil {
			return nil
		}

		b := b1.Bucket([]byte(group.LogCurName))
		if b == nil {
			return nil
		}

		return b.ForEach(func(k, v []byte) error {
			logItem := LogOneItem{}
			err := json.Unmarshal(v, &logItem)
			if err == nil {
				ret = append(ret, &logItem)
			}

			return nil
		})
	})
}

func LogAppend(ctx *MsgContext, group *GroupInfo, l *LogOneItem) error {
	return ctx.Dice.DB.Update(func(tx *bbolt.Tx) error {
		// Retrieve the users bucket.
		// This should be created when the DB is first opened.
		b0 := tx.Bucket([]byte("logs"))
		b1, err := b0.CreateBucketIfNotExists([]byte(group.GroupId))
		if err != nil {
			return err
		}

		b, err := b1.CreateBucketIfNotExists([]byte(group.LogCurName))
		if err == nil {
			l.Id, _ = b.NextSequence()
			buf, err := json.Marshal(l)
			if err != nil {
				return err
			}

			return b.Put(itob(l.Id), buf)
		}
		return err
	})
}

// itob returns an 8-byte big endian representation of v.
func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}
