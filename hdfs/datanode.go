package hdfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (datanode *DataNode) Run() {

	router := gin.Default()
	router.Use(MwPrometheusHttp)
	// register the `/metrics` route.
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	router.POST("/putchunk", func(c *gin.Context) {
		// c.Request.ParseMultipartForm(32 << 20) //上传最大文件限制32M
		// chunkNum := c.Request.Form.Get("chunkNum") //通过这种方式在gin中也可以读取到POST的参数，ginb
		ReplicaNum := c.PostForm("ReplicaNum")
		fmt.Printf("* ReplicaNum= %s\n", ReplicaNum)
		file, header, err := c.Request.FormFile("putchunk")
		if err != nil {
			c.String(http.StatusBadRequest, "XXX Bad request")
			TDFSLogger.Fatal("XXX DataNode error: ", err)
			return
		}
		filename := header.Filename
		fmt.Println("****************************************")
		fmt.Println(file, err, filename)
		fmt.Println("****************************************")

		//ReplicaNum是下一个将要被使用的chunk
		chunkout, err := os.Create(datanode.DATANODE_DIR + "/chunk-" + ReplicaNum) //在服务器本地新建文件进行存储
		if err != nil {
			fmt.Println("XXX DataNode error at Create chunk file", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
		defer chunkout.Close()
		io.Copy(chunkout, file) //在服务器本地新建文件进行存储

		chunkdata := readFileByBytes(datanode.DATANODE_DIR + "/chunk-" + ReplicaNum)

		hash := sha256.New()
		// if _, err := io.Copy(hash, file); err != nil {fmt.Println("DataNode error at sha256", err.Error())}
		hash.Write(chunkdata)
		hashStr := hex.EncodeToString(hash.Sum(nil))
		fmt.Println("** chunk hash", ReplicaNum, ": %s", hashStr)
		FastWrite(datanode.DATANODE_DIR+"/achunkhashs/chunkhash-"+ReplicaNum, []byte(hashStr))

		//100
		n := datanode.StorageAvail
		datanode.ChunkAvail[0] = datanode.ChunkAvail[n-1]
		datanode.ChunkAvail = datanode.ChunkAvail[0 : n-1]
		datanode.StorageAvail--

		c.String(http.StatusCreated, "PutChunk SUCCESS\n")
	})

	router.GET("/getchunk/:chunknum", func(c *gin.Context) {
		chunknum := c.Param("chunknum")
		num, err := strconv.Atoi(chunknum)
		if err != nil {
			fmt.Println("XXX DataNode error(getchunk) at Atoi parse chunknum to int", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
		fmt.Println("Parsed num: ", num)

		fdata := readFileByBytes(datanode.DATANODE_DIR + "/chunk-" + strconv.Itoa(num))
		c.String(http.StatusOK, string(fdata))
	})

	router.GET("/getchunkhash/:chunknum", func(c *gin.Context) {
		chunknum := c.Param("chunknum")
		num, err := strconv.Atoi(chunknum)
		if err != nil {
			fmt.Println("XXX DataNode error(getchunkhash) at Atoi parse chunknum to int", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
		fmt.Println("Parsed num: ", num)

		fdata := readFileByBytes(datanode.DATANODE_DIR + "/achunkhashs/chunkhash-" + strconv.Itoa(num))
		c.String(http.StatusOK, string(fdata))
	})

	router.DELETE("/delchunk/:chunknum", func(c *gin.Context) {
		chunknum := c.Param("chunknum")
		num, err := strconv.Atoi(chunknum)
		if err != nil {
			fmt.Println("XXX DataNode error at Atoi parse chunknum to int", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
		fmt.Println("Parsed num: ", num)

		CleanFile(datanode.DATANODE_DIR + "/chunk-" + strconv.Itoa(num))
		CleanFile(datanode.DATANODE_DIR + "/achunkhashs/chunkhash-" + strconv.Itoa(num))

		c.String(http.StatusOK, "delete DataNode{*}/chunk-"+strconv.Itoa(num)+" SUCCESS")
	})

	// router.GET("/delchunk/:chunknum", func(c *gin.Context) {
	// 	chunknum := c.Param("chunknum")
	// 	num, err := strconv.Atoi(chunknum)
	// 	if err!=nil{
	// 		fmt.Println("XXX DataNode error at Atoi parse chunknum to int", err.Error())
	// 		TDFSLogger.Fatal("XXX DataNode error: ", err)
	// 	}
	// 	fmt.Println("Parsed num: ", num)

	// 	CleanFile(datanode.DATANODE_DIR+"/chunk-"+strconv.Itoa(num))
	// 	// CleanFile(datanode.DATANODE_DIR+"/achunkhashs/chunkhash-"+strconv.Itoa(num))
	// 	DeleteFile(datanode.DATANODE_DIR+"/achunkhashs/chunkhash-"+strconv.Itoa(num))

	// 	c.String(http.StatusOK, "delete DataNode{*}/chunk-"+strconv.Itoa(num)+" SUCCESS")
	// })

	// router.POST("/putmeta", func(c *gin.Context) {
	// 	ReplicaNum := c.PostForm("ReplicaNum")
	// 	fmt.Printf("*** New DataNode Data = %s\n",ReplicaNum)
	// })

	router.GET("/getmeta", func(c *gin.Context) {
		c.JSON(http.StatusOK, datanode)
	})

	router.Run(":" + strconv.Itoa(datanode.Port))
}

func (datanode *DataNode) SendHeartbeat(){
	defer func () {
		if x := recover(); x != nil{
			TDFSLogger.Fatalf("panic when DataNode %s send heartbeat to namenode, err: %v\n", datanode.Location, x) 
		}
	}()

	//每1min上报一次
	datanode.Ticker = time.NewTicker(time.Minute)

	for{
		<- datanode.Ticker.C
		go func ()  {

			d, err := json.Marshal(datanode)
			if err != nil {
				fmt.Println("json to byte[] error", err)
			}
			// 序列化
			reader := bytes.NewReader(d)
			_, err = http.Post(datanode.NNLocation[0]+"/heartbeat", "application/json", reader)
			if err != nil {
				fmt.Println("http post error", err)
			}
		}()

	}
}

func (datanode *DataNode) SetConfig(port string) {
	//所有NN地址
	dnlocations := []string{"http://localhost:11090"}

	res, err := strconv.Atoi(port)
	if err != nil {
		fmt.Println("XXX DataNode error at Atoi parse Port", err.Error())
		TDFSLogger.Fatal("XXX DataNode error: ", err)
	}
	datanode.DATANODE_DIR = DN_DIR
	datanode.Port = res
	datanode.Location = "http://localhost:" + port
	datanode.StorageTotal = DN_CAPACITY
	datanode.StorageAvail = datanode.StorageTotal
	datanode.NNLocation = dnlocations

	datanode.ChunkAvail = append(datanode.ChunkAvail, 0)
	for i := 1; i < datanode.StorageAvail; i++ {
		datanode.ChunkAvail = append(datanode.ChunkAvail, datanode.StorageTotal-i)
	}

	datanode.LastEdit = time.Now().Unix()
	for num := 0; num < datanode.StorageTotal; num++ {
		CreateFile(datanode.DATANODE_DIR + "/chunk-" + strconv.Itoa(num))
	}
	fmt.Println("************************************************************")
	fmt.Println("************************************************************")
	fmt.Printf("*** Successfully Set Config data for a datanode\n")
	datanode.ShowInfo()
	fmt.Println("************************************************************")
	fmt.Println("************************************************************")
}

//目前是datanode断电就删除原来的数据
func (datanode *DataNode) Reset() {
	var i int = 0
	for i < datanode.StorageTotal {
		CleanFile(datanode.DATANODE_DIR + "/chunk-" + strconv.Itoa(i))
		i++
	}

	exist, err := PathExists(datanode.DATANODE_DIR + "/achunkhashs")
	if err != nil {
		fmt.Println("XXX DataNode error at Get Dir chunkhashs", err.Error())
		TDFSLogger.Fatal("XXX DataNode error: ", err)
	}
	if !exist {
		// 不存在创建chunkhash
		err = os.MkdirAll(datanode.DATANODE_DIR+"/achunkhashs", os.ModePerm)
		if err != nil {
			fmt.Println("XXX DataNode error at MkdirAll chunkhashs", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
	} else {
		// 存在首先删除然后创建chunkhash
		err := os.RemoveAll(datanode.DATANODE_DIR + "/achunkhashs")
		if err != nil {
			fmt.Println("XXX DataNode error at RemoveAll file hash data", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}

		err = os.MkdirAll(datanode.DATANODE_DIR+"/achunkhashs", os.ModePerm)
		if err != nil {
			fmt.Println("XXX DataNode error at MkdirAll chunkhashs", err.Error())
			TDFSLogger.Fatal("XXX DataNode error: ", err)
		}
	}
}

func (datanode *DataNode) ShowInfo() {
	fmt.Printf("Location: %s\n", datanode.Location)
	fmt.Printf("DATANODE_DIR: %s\n", datanode.DATANODE_DIR)
	fmt.Printf("Port: %d\n", datanode.Port)
	fmt.Printf("StorageTotal: %d\n", datanode.StorageTotal)
	fmt.Printf("StorageAvail: %d\n", datanode.StorageAvail)
	fmt.Printf("ChunkAvail: %d\n", datanode.ChunkAvail)
	fmt.Printf("LastEdit: %d\n", datanode.LastEdit)
}
