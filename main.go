package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"plugin"
	"runtime"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/gateway"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/vaporvee/acecore/log2webhook"
	"github.com/vaporvee/acecore/shared"
	"github.com/vaporvee/acecore/web"
)

var (
	db *sql.DB
)

var listeners []func()

func main() {
	logrusInitFile()
	var err error
	godotenv.Load()
	connStr := "postgresql://" + os.Getenv("DB_USER") + ":" + url.QueryEscape(os.Getenv("DB_PASSWORD")) + "@" + os.Getenv("DB_SERVER") + ":" + string(os.Getenv("DB_PORT")) + "/" + os.Getenv("DB_NAME") + "?sslmode=disable&application_name=Discord Bot"
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		logrus.Fatal(err)
	}
	err = loadPlugins("plugins/")
	if err != nil {
		logrus.Warn(err)
	}
	shared.BotConfigs = append(shared.BotConfigs,
		bot.WithEventListenerFunc(ready),
		bot.WithEventListenerFunc(applicationCommandInteractionCreate),
		bot.WithEventListenerFunc(autocompleteInteractionCreate),
		bot.WithEventListenerFunc(componentInteractionCreate),
		bot.WithEventListenerFunc(modalSubmitInteractionCreate),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds,
				gateway.IntentGuildEmojisAndStickers,
				gateway.IntentGuildMessages,
				gateway.IntentGuildMembers,
				gateway.IntentDirectMessages,
			),
		))
	client, err := disgo.New(os.Getenv("BOT_TOKEN"),
		shared.BotConfigs...,
	)
	if err != nil {
		logrus.Fatal("error creating Discord session,", err)
		return
	} else {
		logrus.Info("Discord session created")
	}

	if err = client.OpenGateway(context.TODO()); err != nil {
		logrus.Error("error opening connection,", err)
		return
	}
	app, err := client.Rest().GetCurrentApplication()
	if err != nil {
		logrus.Error(err)
	}
	logrus.Infof("Bot is now running as '%s'!", app.Bot.Username)
	go web.HostRoutes(app.Bot.ID.String())

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	logrus.Info("Shutting down...")
}

func logrusInitFile() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.SetReportCaller(true)
	timestamp := time.Now().Unix()

	var file_name string = "logs/bot." + strconv.FormatInt(timestamp, 10) + ".log"
	if _, err := os.Stat("logs"); os.IsNotExist(err) {
		err := os.Mkdir("logs", 0755)
		if err != nil {
			logrus.Error(err)
			return
		}
	}
	log, err := os.OpenFile(file_name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logrus.Error(err)
		return
	}

	mw := io.MultiWriter(os.Stdout, log, &log2webhook.WebhookWriter{})
	logrus.SetOutput(mw)
}

func loadPlugins(directory string) error {
	files, err := os.ReadDir(directory)
	if err != nil {
		return err
	}

	// Determine the appropriate file extension for dynamic libraries
	var ext string
	switch runtime.GOOS {
	case "windows":
		ext = ".dll"
	case "linux":
		ext = ".so"
	case "darwin":
		ext = ".dylib"
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ext {
			p, err := plugin.Open(filepath.Join(directory, file.Name()))
			if err != nil {
				return err
			}

			symPlugin, err := p.Lookup("Plugin")
			if err != nil {
				logrus.Errorf("Error looking up symbol 'Plugin' in %s: %v", file.Name(), err)
				continue
			}

			pluginPtr, ok := symPlugin.(**shared.Plugin)
			if !ok {
				logrus.Errorf("Plugin does not match expected type")
				continue
			}

			plugin := *pluginPtr
			if plugin.Name == "" {
				logrus.Warn("Plugin is unnamed")
			}
			if plugin.Commands != nil {
				commands = append(commands, plugin.Commands...)
			} else {
				logrus.Errorf("Plugin %s has no commands set", plugin.Name)
				continue
			}
			if plugin.Init != nil {
				err = plugin.Init(db)
				if err != nil {
					logrus.Errorf("Error running plugin register %s function: %v", plugin.Name, err)
					continue
				}
			}

		}
	}

	return nil
}
