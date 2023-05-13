package main

import (
	"encoding/json"
	"fmt"
	"github.com/multiplay/go-ts3"
	"go.uber.org/zap"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"
)

var re = regexp.MustCompile(`client_idle_time=(\d+)`)

type Config struct {
	UserName        string
	Password        string
	ServerId        int
	Url             string
	AfkChannelName  string
	MaxIdleTimeMs   int
	IgnoredChannels []string
}

func parseEnvConfig() Config {
	var err error
	var found bool

	var userName string
	userName, found = os.LookupEnv("TS3_USER")
	if !found {
		zap.S().Fatal("TS3_USER not set")
	}
	var password string
	password, found = os.LookupEnv("TS3_PASSWORD")
	if !found {
		zap.S().Fatal("TS3_PASSWORD not set")
	}
	var url string
	url, found = os.LookupEnv("TS3_URL")
	if !found {
		zap.S().Fatal("TS3_URL not set")
	}
	var serverId int
	var serverIdStr string
	serverIdStr, found = os.LookupEnv("TS3_SERVER_ID")
	if !found {
		zap.S().Fatal("TS3_SERVER_ID not set")
	}
	serverId, err = strconv.Atoi(serverIdStr)
	if err != nil {
		zap.S().Fatal("TS3_SERVER_ID is not a number")
	}
	var afkChannelName string
	afkChannelName, found = os.LookupEnv("TS3_AFK_CHANNEL_NAME")
	if !found {
		zap.S().Fatal("TS3_AFK_CHANNEL_NAME not set")
	}

	var maxIdleTimeMS int
	var maxIdleTimeStr string
	maxIdleTimeStr, found = os.LookupEnv("TS3_MAX_IDLE_TIME_SEC")
	if !found {
		zap.S().Fatal("TS3_MAX_IDLE_TIME_SEC not set")
	}
	maxIdleTimeMS, err = strconv.Atoi(maxIdleTimeStr)
	if err != nil {
		zap.S().Fatal("TS3_MAX_IDLE_TIME_SEC is not a number")
	}
	maxIdleTimeMS *= 1000

	var ignoredChannelsRaw string
	ignoredChannelsRaw, found = os.LookupEnv("TS3_IGNORED_CHANNELS")
	if !found {
		ignoredChannelsRaw = "[]"
	}

	// Parse json array
	var ignoredChannels []string
	err = json.Unmarshal([]byte(ignoredChannelsRaw), &ignoredChannels)
	if err != nil {
		zap.S().Fatal("TS3_IGNORED_CHANNELS is not a valid json array")
	}

	return Config{
		UserName:        userName,
		Password:        password,
		Url:             url,
		AfkChannelName:  afkChannelName,
		MaxIdleTimeMs:   maxIdleTimeMS,
		IgnoredChannels: ignoredChannels,
		ServerId:        serverId,
	}
}
func initLogging() {
	logger, err := zap.NewDevelopment(zap.Development())
	if err != nil {
		panic(err)
	}
	zap.ReplaceGlobals(logger)

}

func main() {
	initLogging()
	zap.S().Info("Starting ts3-afk-mover")
	config := parseEnvConfig()

	c, err := ts3.NewClient(config.Url)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	if err = c.Login(config.UserName, config.Password); err != nil {
		log.Fatal(err)
	}

	err = c.Use(config.ServerId)
	if err != nil {
		return
	}

	whoami, err := c.Whoami()
	if err != nil {
		log.Fatal(err)
	}
	zap.S().Info("%v", whoami)

	for {
		var afkChannelId int
		var allowedIdleChannels []int

		channels, err := c.Server.ChannelList()
		if err != nil {
			log.Fatal(err)
		}
		zap.S().Info("Channels")
		for _, channel := range channels {
			if channel.ChannelName == config.AfkChannelName {
				afkChannelId = channel.ID
			}
			for _, ignoredChannel := range config.IgnoredChannels {
				if channel.ChannelName == ignoredChannel {
					allowedIdleChannels = append(allowedIdleChannels, channel.ID)
					zap.S().Infof("Ignoring channel %s [%d]", channel.ChannelName, channel.ID)
				}
			}
			zap.S().Infof("%s", channel.ChannelName)
		}
		if afkChannelId == 0 {
			zap.S().Fatal("afk channel not found")
		}

		clients, err := c.Server.ClientList()
		if err != nil {
			log.Fatal(err)
		}

		for _, client := range clients {
			zap.S().Info("%v", client)
			exec, err := c.Server.Exec(fmt.Sprintf("clientinfo clid=%d", client.ID))
			if err != nil {
				zap.S().Error(err)
				continue
			}

			// extract client_idle_time=<number> from exec
			matches := re.FindStringSubmatch(exec[0])
			if len(matches) != 2 {
				zap.S().Error("client_idle_time not found")
				continue
			}

			idleTime, err := strconv.Atoi(matches[1])
			if err != nil {
				zap.S().Error(err)
				continue
			}
			if idleTime > config.MaxIdleTimeMs {
				if contains(allowedIdleChannels, client.ChannelID) {
					zap.S().Infof("User %s is idle for %d seconds, but in allowed channel", client.Nickname, idleTime/1000)
					continue
				}
				if client.ChannelID == afkChannelId {
					zap.S().Infof("User %s is idle for %d seconds, but already in afk channel", client.Nickname, idleTime/1000)
					continue
				}

				zap.S().Infof("User %s is idle for %d seconds", client.Nickname, idleTime/1000)
				zap.S().Info("moving client to afk channel")
				_, err = c.Server.Exec(fmt.Sprintf("clientmove clid=%d cid=%d", client.ID, afkChannelId))
				if err != nil {
					zap.S().Error(err)
				}
			}
		}
		time.Sleep(10 * time.Second)
	}

}

func contains(channels []int, id int) bool {
	for _, channel := range channels {
		if channel == id {
			return true
		}
	}
	return false
}
