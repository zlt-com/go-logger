package logger

import (
	"database/sql/driver"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jinzhu/gorm"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	"github.com/zlt-com/go-common"
	"github.com/zlt-com/go-config"
	"github.com/zlt-com/go-es"
	elogrus "gopkg.in/sohlich/elogrus.v7"
)

// MyLogger MyLogger
type MyLogger struct {
}

var (
	today = common.Timestamp2Time(time.Now().Unix(), common.Date)
	//elasticSearch 6.x后每个索引只能有一个type，所以是多个索引
	sqlIndex   = strings.Join([]string{config.Config.ElasticSQLLogName, today}, "-")
	errIndex   = strings.Join([]string{config.Config.ElasticErrorLogName, today}, "-")
	infoIndex  = strings.Join([]string{config.Config.ElasticInfoLogName, today}, "-")
	loginIndex = strings.Join([]string{config.Config.ElasticLoginLogName, today}, "-")

	sqlLog   = logrus.New()
	infoLog  = logrus.New()
	errLog   = logrus.New()
	loginLog = logrus.New()
	fileLog  = logrus.New()
)

// Start 启动日志，日志记录在elastic和文件
func Start() {
	sqlLevelHook, err := elogrus.NewAsyncElasticHook(es.EsClient, config.Config.ElasticSourceHost, logrus.InfoLevel, sqlIndex)
	if err != nil {
		fileLog.Error(err)
	}
	infoLevelHook, err := elogrus.NewAsyncElasticHook(es.EsClient, config.Config.ElasticSourceHost, logrus.InfoLevel, infoIndex)
	if err != nil {
		fileLog.Error(err)
	}
	errorLevelHook, err := elogrus.NewAsyncElasticHook(es.EsClient, config.Config.ElasticSourceHost, logrus.ErrorLevel, errIndex)
	if err != nil {
		fileLog.Error(err)
	}
	loginLevelHook, err := elogrus.NewAsyncElasticHook(es.EsClient, config.Config.ElasticSourceHost, logrus.InfoLevel, loginIndex)
	if err != nil {
		fileLog.Error(err)
	}
	sqlLog.Hooks.Add(sqlLevelHook)
	infoLog.Hooks.Add(infoLevelHook)
	errLog.Hooks.Add(errorLevelHook)
	loginLog.Hooks.Add(loginLevelHook)
	fileLog.Hooks.Add(newLfsHook(config.Config.LogFile, 100))
	fileLog.SetLevel(logrus.InfoLevel)
}

func newLfsHook(logName string, maxRemainCnt uint) logrus.Hook {
	writer, err := rotatelogs.New(
		logName+".%Y%m%d%H",
		// WithLinkName为最新的日志建立软连接，以方便随着找到当前日志文件
		rotatelogs.WithLinkName(logName),

		// WithRotationTime设置日志分割的时间，这里设置为一小时分割一次
		rotatelogs.WithRotationTime(time.Hour*24),

		// WithMaxAge和WithRotationCount二者只能设置一个，
		// WithMaxAge设置文件清理前的最长保存时间，
		// WithRotationCount设置文件清理前最多保存的个数。
		//rotatelogs.WithMaxAge(time.Hour*24),
		rotatelogs.WithRotationCount(maxRemainCnt),
	)

	if err != nil {
		logrus.Errorf("config local file system for logger error: %v", err)
	}

	// level, ok := logLevels[*logLevel]

	// if ok {
	// 	logrus.SetLevel(level)
	// } else {
	logrus.SetLevel(logrus.InfoLevel)
	// }

	lfsHook := lfshook.NewHook(lfshook.WriterMap{
		logrus.DebugLevel: writer,
		logrus.InfoLevel:  writer,
		logrus.WarnLevel:  writer,
		logrus.ErrorLevel: writer,
		logrus.FatalLevel: writer,
		logrus.PanicLevel: writer,
	}, &logrus.JSONFormatter{})

	return lfsHook
}

// Error Error
func Error(args ...interface{}) {
	errLog.WithField("category", "error").Error(args...)
	fileLog.WithField("category", "error").Error(args...)
}

// Info INFO
func Info(category string, args ...interface{}) {
	infoLog.WithField("category", category).Info(args...)
	fileLog.WithField("category", category).Info(args...)
}

// SQL SQL
func SQL(category string, args string) {
	sqlLog.WithField("category", category).Info(args)
	fileLog.WithField("category", category).Info(args)
}

// Login Login
func Login(category string, args map[string]interface{}) {
	loginLog.WithField("category", category).WithFields(args).Info()
	fileLog.WithField("category", category).WithFields(args).Info()
}

// func Login2(category string, args ...interface{}) {
// 	loginLog.WithField("category", category).Info(args...)
// }

// Print Print
func (logger *MyLogger) Print(values ...interface{}) {
	level := values[0]
	if level == "sql" {
		msg := LogFormatter(values...)
		if sql, ok := msg[3].(string); ok {
			sql = strings.Trim(sql, " ")
			if strings.Index(sql, "INSERT") == 0 {
				SQL("INSERT", sql)
			} else if strings.Index(sql, "UPDATE") == 0 {
				SQL("UPDATE", sql)
			} else if strings.Index(sql, "DELETE") == 0 {
				SQL("DELETE", sql)
			} else if strings.Index(sql, "SELECT") == 0 {
				SQL("SELECT", sql)
			}
		}
	} else {
		Info("gorm-other", values)
	}
}

func isPrintable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

var (
	sqlRegexp                = regexp.MustCompile(`\?`)
	numericPlaceHolderRegexp = regexp.MustCompile(`\$\d+`)
)

// LogFormatter 格式化sql
var LogFormatter = func(values ...interface{}) (messages []interface{}) {
	if len(values) > 1 {
		var (
			sql             string
			formattedValues []string
			level           = values[0]
			currentTime     = "\n\033[33m[" + gorm.NowFunc().Format("2006-01-02 15:04:05") + "]\033[0m"
			source          = fmt.Sprintf("\033[35m(%v)\033[0m", values[1])
		)

		messages = []interface{}{source, currentTime}

		if level == "sql" {
			// duration
			messages = append(messages, fmt.Sprintf(" \033[36;1m[%.2fms]\033[0m ", float64(values[2].(time.Duration).Nanoseconds()/1e4)/100.0))
			// sql

			for _, value := range values[4].([]interface{}) {
				indirectValue := reflect.Indirect(reflect.ValueOf(value))
				if indirectValue.IsValid() {
					value = indirectValue.Interface()
					if t, ok := value.(time.Time); ok {
						formattedValues = append(formattedValues, fmt.Sprintf("'%v'", t.Format("2006-01-02 15:04:05")))
					} else if b, ok := value.([]byte); ok {
						if str := string(b); isPrintable(str) {
							formattedValues = append(formattedValues, fmt.Sprintf("'%v'", str))
						} else {
							formattedValues = append(formattedValues, "'<binary>'")
						}
					} else if r, ok := value.(driver.Valuer); ok {
						if value, err := r.Value(); err == nil && value != nil {
							formattedValues = append(formattedValues, fmt.Sprintf("'%v'", value))
						} else {
							formattedValues = append(formattedValues, "NULL")
						}
					} else {
						switch value.(type) {
						case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
							formattedValues = append(formattedValues, fmt.Sprintf("%v", value))
						default:
							formattedValues = append(formattedValues, fmt.Sprintf("'%v'", html.EscapeString(value.(string))))
						}
					}
				} else {
					formattedValues = append(formattedValues, "NULL")
				}
			}

			// differentiate between $n placeholders or else treat like ?
			if numericPlaceHolderRegexp.MatchString(values[3].(string)) {
				sql = values[3].(string)
				for index, value := range formattedValues {
					placeholder := fmt.Sprintf(`\$%d([^\d]|$)`, index+1)
					sql = regexp.MustCompile(placeholder).ReplaceAllString(sql, value+"$1")
				}
			} else {
				formattedValuesLength := len(formattedValues)
				for index, value := range sqlRegexp.Split(values[3].(string), -1) {
					sql += value
					if index < formattedValuesLength {
						sql += formattedValues[index]
					}
				}
			}

			messages = append(messages, sql)
			messages = append(messages, fmt.Sprintf(" \n\033[36;31m[%v]\033[0m ", strconv.FormatInt(values[5].(int64), 10)+" rows affected or returned "))
		} else {
			messages = append(messages, "\033[31;1m")
			messages = append(messages, values[2:]...)
			messages = append(messages, "\033[0m")
		}
	}

	return
}
