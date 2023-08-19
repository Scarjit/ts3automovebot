package main

import (
	"encoding/json"
	"fmt"
	"github.com/multiplay/go-ts3"
	"go.uber.org/zap"
	"os"
	"regexp"
	"strconv"
	"time"
)

var idleTimeRegex = regexp.MustCompile(`client_idle_time=(\d+)`)
var recentJoins = make(map[int]time.Time)

type Config struct {
	UserName         string
	Password         string
	ServerId         int
	Url              string
	AfkChannelName   string
	MaxIdleTimeMs    int
	IgnoredChannels  []string
	AllowGracePeriod bool
}

func loadConfigFromEnv() (Config, error) {
	config := Config{}
	var err error

	config.UserName, err = getRequiredEnv("TS3_USER")
	if err != nil {
		return config, err
	}

	config.Password, err = getRequiredEnv("TS3_PASSWORD")
	if err != nil {
		return config, err
	}

	config.Url, err = getRequiredEnv("TS3_URL")
	if err != nil {
		return config, err
	}

	serverIdStr, err := getRequiredEnv("TS3_SERVER_ID")
	if err != nil {
		return config, err
	}

	config.ServerId, err = strconv.Atoi(serverIdStr)
	if err != nil {
		return config, fmt.Errorf("TS3_SERVER_ID is not a number: %v", err)
	}

	config.AfkChannelName, err = getRequiredEnv("TS3_AFK_CHANNEL_NAME")
	if err != nil {
		return config, err
	}

	maxIdleTimeStr, err := getRequiredEnv("TS3_MAX_IDLE_TIME_SEC")
	if err != nil {
		return config, err
	}

	config.MaxIdleTimeMs, err = strconv.Atoi(maxIdleTimeStr)
	if err != nil {
		return config, fmt.Errorf("TS3_MAX_IDLE_TIME_SEC is not a number: %v", err)
	}
	config.MaxIdleTimeMs *= 1000

	ignoredChannelsRaw, err := getRequiredEnv("TS3_IGNORED_CHANNELS")
	if err != nil {
		return config, err
	}

	err = json.Unmarshal([]byte(ignoredChannelsRaw), &config.IgnoredChannels)
	if err != nil {
		return config, fmt.Errorf("TS3_IGNORED_CHANNELS is not a valid json array: %v", err)
	}

	allowGracePeriod, err := getRequiredEnv("TS3_ALLOW_GRACE_PERIOD")
	if err != nil {
		return config, err
	}

	config.AllowGracePeriod, err = strconv.ParseBool(allowGracePeriod)
	if err != nil {
		return config, fmt.Errorf("TS3_ALLOW_GRACE_PERIOD is not a boolean: %v", err)
	}

	return config, nil
}

func getRequiredEnv(key string) (string, error) {
	value, found := os.LookupEnv(key)
	if !found {
		return "", fmt.Errorf("%s not set", key)
	}
	return value, nil
}

func setupLogging() error {
	logger, err := zap.NewDevelopment(zap.Development())
	if err != nil {
		return err
	}
	zap.ReplaceGlobals(logger)
	return nil
}

func handleError(err error) {
	zap.S().Error(err)
	time.Sleep(1 * time.Minute)
	panic(err)
}

func main() {
	err := setupLogging()
	if err != nil {
		handleError(err)
	}

	zap.S().Info("Starting ts3-afk-mover")
	config, err := loadConfigFromEnv()
	if err != nil {
		handleError(err)
	}

	client, err := ts3.NewClient(config.Url)
	if err != nil {
		handleError(err)
	}
	defer client.Close()

	if err = client.Login(config.UserName, config.Password); err != nil {
		zap.S().Fatal(err)
	}

	err = client.Use(config.ServerId)
	if err != nil {
		zap.S().Fatal(err)
	}

	err = client.SetNick(config.UserName)
	if err != nil {
		zap.S().Warn(err)
	}

	whoami, err := client.Whoami()
	if err != nil {
		zap.S().Fatal(err)
	}

	zap.S().Info("%v", whoami)

	for {
		processClients(client, config)
		time.Sleep(10 * time.Second)
	}
}

func isChannelIgnored(channels []int, id int) bool {
	for _, channel := range channels {
		if channel == id {
			return true
		}
	}
	return false
}

func processClients(client *ts3.Client, config Config) {
	// Get the list of channels.
	channels, err := client.Server.ChannelList()
	if err != nil {
		zap.S().Errorf("Error getting channel list: %v", err)
		time.Sleep(5 * time.Second)
		return
	}

	var afkChannelId int
	var allowedIdleChannels []int

	for _, channel := range channels {
		if channel.ChannelName == config.AfkChannelName {
			afkChannelId = channel.ID
		}

		for _, ignoredChannel := range config.IgnoredChannels {
			if channel.ChannelName == ignoredChannel {
				allowedIdleChannels = append(allowedIdleChannels, channel.ID)
				//zap.S().Infof("Ignoring channel %s [%d]", channel.ChannelName, channel.ID)
			}
		}
	}

	if afkChannelId == 0 {
		zap.S().Fatal("afk channel not found")
	}

	// Get the list of clients.
	clients, err := client.Server.ClientList()
	if err != nil {
		zap.S().Errorf("Error getting c list: %v", err)
		time.Sleep(5 * time.Second)
		return
	}

	for _, c := range clients {
		// If the client is in a channel that had a recent join, ignore their idle time for 10 seconds.
		if joinTime, ok := recentJoins[c.ChannelID]; ok {
			if time.Since(joinTime) <= 10*time.Second {
				zap.S().Infof("User %s's idle time ignored for 10 seconds due to recent join", c.Nickname)
				continue
			}
		}

		exec, err := client.Server.Exec(fmt.Sprintf("clientinfo clid=%d", c.ID))
		if err != nil {
			zap.S().Error(err)
			continue
		}

		// Extract client_idle_time=<number> from exec
		matches := idleTimeRegex.FindStringSubmatch(exec[0])
		if len(matches) != 2 {
			zap.S().Error("client_idle_time not found")
			continue
		}

		for _, c := range clients {
			// If the client is in a channel that had a recent join, ignore their idle time for 10 seconds.
			if joinTime, ok := recentJoins[c.ChannelID]; ok {
				if time.Since(joinTime) <= 10*time.Second {
					zap.S().Infof("User %s's idle time ignored for 10 seconds due to recent join", c.Nickname)
					continue
				}
			}

			exec, err := client.Server.Exec(fmt.Sprintf("clientinfo clid=%d", c.ID))
			if err != nil {
				zap.S().Error(err)
				continue
			}

			// Extract client_idle_time=<number> from exec
			matches := idleTimeRegex.FindStringSubmatch(exec[0])
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
				if isChannelIgnored(allowedIdleChannels, c.ChannelID) {
					zap.S().Infof("User %s is idle for %d seconds, but in allowed channel", c.Nickname, idleTime/1000)
					continue
				}
				if c.ChannelID == afkChannelId {
					zap.S().Infof("User %s is idle for %d seconds, but already in afk channel", c.Nickname, idleTime/1000)
					continue
				}

				// Check if a user is solo in a channel
				isSolo := true
				for _, c2 := range clients {
					if c2.ChannelID == c.ChannelID && c2.ID != c.ID {
						isSolo = false
						break
					}
				}
				if isSolo {
					zap.S().Infof("User %s is idle for %d seconds, but solo in channel", c.Nickname, idleTime/1000)
					continue
				}

				zap.S().Infof("User %s is idle for %d seconds", c.Nickname, idleTime/1000)
				zap.S().Info("moving c to afk channel")
				_, err = client.Server.Exec(fmt.Sprintf("clientmove clid=%d cid=%d", c.ID, afkChannelId))
				if err != nil {
					zap.S().Error(err)
				}
			}
		}
	}
}
