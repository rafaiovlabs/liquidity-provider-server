package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rsksmart/liquidity-provider-server/connectors"
	"github.com/rsksmart/liquidity-provider-server/http"
	"github.com/rsksmart/liquidity-provider-server/storage"
	"github.com/rsksmart/liquidity-provider/providers"
	log "github.com/sirupsen/logrus"
	"github.com/tkanos/gonfig"
)

var (
	cfg config
	srv http.Server
)

func loadConfig() {
	err := gonfig.GetConf("config.json", &cfg)

	if err != nil {
		log.Fatalf("error loading config file: %v", err)
	}
}

func initLogger() {
	if cfg.Debug {
		log.SetLevel(log.DebugLevel)
	}
	if cfg.LogFile == "" {
		return
	}
	f, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)

	if err != nil {
		log.Error(fmt.Sprintf("cannot open file %v: ", cfg.LogFile), err)
	} else {
		log.SetOutput(f)
	}
}

func startServer(rsk *connectors.RSK, btc *connectors.BTC, db *storage.DB) {
	lp, err := providers.NewLocalProvider(cfg.Provider)
	if err != nil {
		log.Fatal("cannot create local provider: ", err)
	}

	srv = http.New(rsk, btc, db)
	log.Debug("registering local provider (this might take a while)")
	err = srv.AddProvider(lp)
	if err != nil {
		log.Fatalf("error registering local provider: %v", err)
	}
	port := cfg.Server.Port

	if port == 0 {
		port = 8080
	}
	go func() {
		err := srv.Start(port)

		if err != nil {
			log.Error("server error: ", err.Error())
		}
	}()
}

func initFederation(rsk connectors.RSK) (*connectors.FedInfo, error) {
	log.Debug("getting federation info")
	fedSize, err := rsk.GetFedSize()
	if err != nil {
		return nil, err
	}

	var pubKeys []string
	for i := 0; i < fedSize; i++ {
		pubKey, err := rsk.GetFedPublicKey(i)
		if err != nil {
			log.Error("error fetching fed public key: ", err.Error())
			return nil, err
		}
		pubKeys = append(pubKeys, pubKey)
	}

	fedThreshold, err := rsk.GetFedThreshold()
	if err != nil {
		log.Error("error fetching federation size: ", err.Error())
		return nil, err
	}

	fedAddress, err := rsk.GetFedAddress()
	if err != nil {
		return nil, err
	}

	activeFedBlockHeight, err := rsk.GetActiveFederationCreationBlockHeight()
	if err != nil {
		log.Error("error fetching federation address: ", err.Error())
		return nil, err
	}

	return &connectors.FedInfo{
		FedThreshold:         fedThreshold,
		FedSize:              fedSize,
		PubKeys:              pubKeys,
		FedAddress:           fedAddress,
		ActiveFedBlockHeight: activeFedBlockHeight,
		IrisActivationHeight: cfg.IrisActivationHeight,
		ErpKeys:              cfg.ErpKeys,
	}, nil
}

func main() {
	loadConfig()
	initLogger()
	rand.Seed(time.Now().UnixNano())

	log.Info("starting liquidity provider server")
	log.Debugf("loaded config %+v", cfg)

	db, err := storage.Connect(cfg.DB.Path)
	if err != nil {
		log.Fatal("error connecting to DB: ", err)
	}

	rsk, err := connectors.NewRSK(cfg.RSK.LBCAddr, cfg.RSK.BridgeAddr, cfg.RSK.RequiredBridgeConfirmations)
	if err != nil {
		log.Fatal("RSK error: ", err)
	}

	err = rsk.Connect(cfg.RSK.Endpoint)
	if err != nil {
		log.Fatal("error connecting to RSK: ", err)
	}

	fedInfo, err := initFederation(*rsk)
	if err != nil {
		log.Fatal("error initializing federation info: ", err)
	}

	btc, err := connectors.NewBTC(cfg.BTC.Network, *fedInfo)
	if err != nil {
		log.Fatal("error initializing BTC connector: ", err)
	}

	err = btc.Connect(cfg.BTC.Endpoint, cfg.BTC.Username, cfg.BTC.Password)
	if err != nil {
		log.Fatal("error connecting to BTC: ", err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	startServer(rsk, btc, db)

	<-done

	srv.Shutdown()
	db.Close()
	rsk.Close()
	btc.Close()
}
