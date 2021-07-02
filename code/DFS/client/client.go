package client

import (
	"DFS/util"
	"DFS/util/zkWrap"
	json "encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

type Client struct {
	clientAddr      util.Address
	masterAddr      util.Address
	fdTable         map[int]util.DFSPath
	s               *http.Server
	LeaderHeartbeat *zkWrap.Heartbeat // only one master(leader) and some clients in this room
	//TODO:add lease
}

// InitClient initClient init a new client and return.
func InitClient(clientAddr util.Address, masterAddr util.Address) *Client {
	c := &Client{
		clientAddr: clientAddr,
		masterAddr: masterAddr, //TODO: we should not use this arg
		fdTable:    make(map[int]util.DFSPath),
	}
	//to find master leader
	zkWrap.Chroot("/DFS")
	c.RegisterNodes()
	return c
}

func (c *Client) Serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/create", c.Create)
	mux.HandleFunc("/mkdir", c.Mkdir)
	mux.HandleFunc("/delete", c.Delete)
	mux.HandleFunc("/read", c.Read)
	mux.HandleFunc("/write", c.Write)
	mux.HandleFunc("/open", c.Open)
	mux.HandleFunc("/close", c.Close)
	mux.HandleFunc("/append", c.Append)
	// mux.HandleFunc("/cappend", c.ConcurrentAppend)
	mux.HandleFunc("/list", c.List)
	mux.HandleFunc("/scan", c.Scan)
	mux.HandleFunc("/fileInfo", c.GetFileInfo)
	c.s = &http.Server{
		Addr:           util.CLIENTADDR,
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	err := c.s.ListenAndServe()
	if err != nil {
		logrus.Debug("Client server shutdown!\n")
	}
	//logrus.Fatalln("stop!")
}

// Exit Directly
func (c *Client) Exit() {
	err := c.s.Close()
	if err != nil {
		return
	}
}

// TODO:client should not return error due to DFS failure
// Create a file.
func (c *Client) Create(w http.ResponseWriter, r *http.Request) {
	var arg util.CreateArg
	var ret util.CreateRet
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err = util.Call(string(c.masterAddr), "Master.CreateRPC", arg, &ret)
	if err != nil {
		logrus.Fatalln("CreateRPC failed:", err)
		return
	}
	io.WriteString(w, "0")
	return
}

// Mkdir a dir.
func (c *Client) Mkdir(w http.ResponseWriter, r *http.Request) {
	var arg util.MkdirArg
	var ret util.MkdirRet
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err = util.Call(string(c.masterAddr), "Master.MkdirRPC", arg, &ret)
	if err != nil {
		logrus.Fatalln("MkdirRPC failed:", err)
		return
	}
	io.WriteString(w, "0")
	return
}

// Open a file.
// If fd is depleted, return -1
func (c *Client) Open(w http.ResponseWriter, r *http.Request) {
	var arg util.OpenArg
	var ret util.OpenRet
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for i := 0; i < util.MAXFD; i++ {
		_, exist := c.fdTable[i]
		if !exist {
			logrus.Debugf("Client open : assign %d", i)
			c.fdTable[i] = arg.Path
			io.WriteString(w, strconv.Itoa(i))
			return
		}
	}
	w.WriteHeader(400)
	msg, _ := json.Marshal(ret)
	w.Write(msg)
}

// Close a file.
func (c *Client) Close(w http.ResponseWriter, r *http.Request) {
	var arg util.CloseArg
	err := json.NewDecoder(r.Body).Decode(&arg)
	defer func(err error) {
		if err != nil {
			w.Write([]byte(err.Error()))
		}
	}(err)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, exist := c.fdTable[arg.Fd]
	if !exist {
		err = fmt.Errorf("FileClosedError : file has been closed\n")
		print(err.Error())
		http.Error(w, "Fd Nonexist", http.StatusBadRequest)
		return
	}
	logrus.Debugf("Client close : free %d", arg.Fd)
	delete(c.fdTable, arg.Fd)
	return
}

// func (c *Client) _Read(path util.DFSPath, offset int, len int, fileSize int) (readBytes int, buf []byte, err error) {
func (c *Client) _Read(path util.DFSPath, offset int, len int) (readBytes int, buf []byte, err error) {
	var argR util.GetReplicasArg
	var retR util.GetReplicasRet
	var argRCK util.ReadChunkArgs
	var retRCK util.ReadChunkReply
	argR.Path = path
	for readBytes < len {
		roundOff := (offset + readBytes) % util.MAXCHUNKSIZE
		roundReadBytes := int(math.Min(float64(util.MAXCHUNKSIZE-roundOff), float64(len-readBytes)))

		argR.ChunkIndex = (offset + readBytes) / util.MAXCHUNKSIZE
		err = util.Call(string(c.masterAddr), "Master.GetReplicasRPC", argR, &retR)
		if err != nil {
			return
		}
		logrus.Debugf(" ChunkHandle : %d Addresses : %s %s %s\n", retR.ChunkHandle, retR.ChunkServerAddrs[0], retR.ChunkServerAddrs[1], retR.ChunkServerAddrs[2])
		//TODO : make it random
		argRCK.Handle = retR.ChunkHandle
		argRCK.Len = roundReadBytes
		argRCK.Off = roundOff
		err = util.Call(string(retR.ChunkServerAddrs[0]), "ChunkServer.ReadChunkRPC", argRCK, &retRCK)
		if err != nil {
			logrus.Panicln("Client read failed :", err)
			return
		}
		if retRCK.Len != roundReadBytes {
			logrus.Panicln("Client should read %v,buf only read %v", roundReadBytes, retRCK.Len)
			return
		}
		buf = append(buf, retRCK.Buf...)
		readBytes += roundReadBytes
		logrus.Debugf(" Read %d bytes from chunkserver %s, bytes read %d\n", roundReadBytes, string(retR.ChunkServerAddrs[0]), readBytes)
	}
	return
}

// Read a file.
// should contact the master first, then get the data directly from chunkserver
func (c *Client) Read(w http.ResponseWriter, r *http.Request) {
	var argR util.ReadArg
	var retR util.ReadRet
	var argF util.GetFileMetaArg
	var retF util.GetFileMetaRet
	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&argR)
	if err != nil {
		logrus.Fatalln("Client read failed :", err)
		w.WriteHeader(400)
		return
	}

	// Get the file metadata and check
	path := c.fdTable[argR.Fd]
	if path == "" {
		err = fmt.Errorf("Client read failed : fd %d is not opened\n", argR.Fd)
		return
	}

	argF.Path = path
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", argF, &retF)
	if err != nil || !retF.Exist {
		logrus.Fatalln("Client read failed :", err)
		return
	}

	// fileSize := retF.Size
	// if argR.Offset+argR.Len > fileSize {
	// 	err = fmt.Errorf("Client read failed : read offset + read len  %d exceed file size %d",argR.Offset+argR.Len,fileSize)
	// 	logrus.Debugln(err)

	// 	return
	// }

	//TODO: check whether offset exceed filesize? (offset > chunknum * chunksize ?)

	// Read to chunk

	// readBytes, buf, err := c._Read(path, argR.Offset, argR.Len, fileSize)
	readBytes, buf, err := c._Read(path, argR.Offset, argR.Len)

	if err != nil {
		logrus.Fatalln("Client read failed :", err)
		w.WriteHeader(400)
		return
	}
	retR.Data = buf
	retR.Len = readBytes
	msg, _ := json.Marshal(retR)
	w.Write(msg)
	return

}

// Delete a file.
func (c *Client) Delete(w http.ResponseWriter, r *http.Request) {
	// var argR util.GetReplicasArg
	// var retR util.GetReplicasRet
	var argF util.GetFileMetaArg
	var retF util.GetFileMetaRet
	var argD util.DeleteArg
	var retD util.DeleteRet

	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&argD)
	if err != nil {
		logrus.Fatalln("Client delete failed :", err)
		w.WriteHeader(400)
		return
	}

	// Get the file metadata and check
	argF.Path = argD.Path
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", argF, &retF)
	if !retF.Exist {
		logrus.Fatalln("Client delete failed :", err)
		w.WriteHeader(400)
		return
	}

	// Delete the master metadata last
	err = util.Call(string(c.masterAddr), "Master.DeleteRPC", argD, &retD)
	if err != nil {
		logrus.Fatalln("Client delete failed :", err)
		w.WriteHeader(400)
		return
	}

	// we don't need to delete chunk at once

	// Delete the chunk one by one
	// By default, the first entry int retR.ChunkServerAddr is the primary
	// chunkIndex := 0
	// for chunkIndex*util.MAXCHUNKSIZE < retF.Size {
	// 	argR.ChunkIndex = chunkIndex
	// 	err = util.Call(string(c.masterAddr), "Master.GetReplicasRPC", argR, &retR)
	// 	if err != nil {
	// 		logrus.Fatalln("Client delete failed :", err)
	// 		w.WriteHeader(400)
	// 		return
	// 	}
	// 	// TODO delete file in chunkServer
	// 	chunkIndex += 1
	// }

	w.WriteHeader(200)
	return
}

// ConcurrentAppend to a file
// func (c *Client) ConcurrentAppend(w http.ResponseWriter, r *http.Request) {
// 	//var argG util.GetFileMetaArg
// 	var retG util.GetFileMetaRet
// 	var argC util.CAppendArg
// 	var retC util.CAppendRet
// 	// Decode the params
// 	err := json.NewDecoder(r.Body).Decode(&argC)
// 	if err != nil {
// 		logrus.Fatalln("Client getFileInfo failed :", err)
// 		w.WriteHeader(400)
// 		return
// 	}
// 	// Get the file metadata and check
// 	path := c.fdTable[argC.Fd]
// 	if path == "" {
// 		err = fmt.Errorf("Client read failed : fd %d is not opened\n", argC.Fd)
// 		return
// 	}
// 	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", util.GetFileMetaArg{Path: path}, &retG)
// 	if err != nil {
// 		logrus.Fatalln("Client concurrent append failed :", err)
// 		w.WriteHeader(400)
// 		return
// 	}
// 	if len(argC.Data) > util.MAXAPPENDSIZE { // append size cannot exist half a chunk
// 		logrus.Fatalln("Client concurrent append failed : append size is too large")
// 		w.WriteHeader(400)
// 		return
// 	}
// 	var offset int
// 	chunkIndex := retG.Size / util.MAXCHUNKSIZE
// 	end := false
// 	// try append to chunk, pad it and retry on next chunk if normal failure
// 	// until success or unexpected error
// 	for !end {
// 		// TODO :finish it
// 		offset, err = c._ConcurrentAppend(chunkIndex, argC.Data)
// 		if err == nil {
// 			end = true
// 		}
// 	}
// 	retC.Offset = offset
// 	msg, _ := json.Marshal(retC)
// 	w.Write(msg)
// 	return
// }

// Append to a file
func (c *Client) Append(w http.ResponseWriter, r *http.Request) {
	var argA util.AppendArg
	var argF util.GetFileMetaArg
	var retF util.GetFileMetaRet

	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&argA)
	if err != nil {
		logrus.Fatalln("Client append failed :", err)
		w.WriteHeader(400)
		return
	}

	//Check append length
	if len(argA.Data) > util.MAXAPPENDSIZE { // append size cannot exist half a chunk
		logrus.Fatalln("Client append failed : append size is too large")
		w.WriteHeader(400)
		return
	}

	// Get the file metadata and check
	path := c.fdTable[argA.Fd]
	if path == "" {
		err = fmt.Errorf("Client write failed : fd %d is not opened\n", argA.Fd)
		return
	}

	argF.Path = path
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", argF, &retF)
	if !retF.Exist {
		logrus.Fatalln("Client write failed :", err)
		return
	}
	// fileSize := retF.Size

	// Write to file
	// writtenBytes, err := c._Write(path, fileSize, argA.Data, fileSize)

	offset, err := c._Append(path, argA.Data)
	if err != nil {
		logrus.Fatalln("Client write failed :", err)
		w.WriteHeader(400)
		return
	}
	msg, _ := json.Marshal(offset)
	w.Write(msg)
	w.WriteHeader(200)
	return

}

func (c *Client) _Write(path util.DFSPath, offset int, data []byte) (writtenBytes int, err error) {
	var argR util.GetReplicasArg
	var retR util.GetReplicasRet
	var argL util.LoadDataArgs
	var retL util.LoadDataReply
	// var argS util.SetFileMetaArg
	// var retS util.SetFileMetaRet
	var argC util.SyncArgs
	var retC util.SyncReply

	// Write the chunk (may add chunks)
	// By default, the first entry int retR.ChunkServerAddr is the primary
	argR.Path = path
	for writtenBytes < len(data) {
		argR.ChunkIndex = (offset + writtenBytes) / util.MAXCHUNKSIZE
		err = util.Call(string(c.masterAddr), "Master.GetReplicasRPC", argR, &retR)
		if err != nil {
			return
		}
		logrus.Debugf(" ChunkHandle : %d Addresses : %s %s %s\n", retR.ChunkHandle, retR.ChunkServerAddrs[0], retR.ChunkServerAddrs[1], retR.ChunkServerAddrs[2])
		roundWrittenBytes := int(math.Min(float64(util.MAXCHUNKSIZE-(offset+writtenBytes)%util.MAXCHUNKSIZE), float64(len(data)-writtenBytes)))
		var cid = util.CacheID{
			Handle:     retR.ChunkHandle,
			ClientAddr: c.clientAddr,
		}
		argL.CID = cid
		argL.Data = data[writtenBytes:(writtenBytes + roundWrittenBytes)]
		// TODO: make it random , now is fixed order
		argL.Addrs = retR.ChunkServerAddrs
		//argL.Addrs = make([]util.Address,0)
		//for _,index := range rand.Perm(len(retR.ChunkServerAddrs)){
		//	argL.Addrs = append(argL.Addrs,retR.ChunkServerAddrs[index])
		//}
		// Send to Master now
		err = util.Call(string(argL.Addrs[0]), "ChunkServer.LoadDataRPC", argL, &retL)
		if err != nil {
			logrus.Fatalln("Client write failed :", err)
			return
		}
		argC = util.SyncArgs{
			CID:      cid,
			Off:      (offset + writtenBytes) % util.MAXCHUNKSIZE,
			Addrs:    retR.ChunkServerAddrs[1:],
			IsAppend: false,
		}
		err = util.Call(string(argL.Addrs[0]), "ChunkServer.SyncRPC", argC, &retC)
		if err != nil {
			logrus.Fatalln("Client write failed :", err)
			return
		}
		writtenBytes += roundWrittenBytes
		logrus.Debugf(" Write %d bytes to chunkserver %s, bytes written %d\n", roundWrittenBytes, argL.Addrs[0], writtenBytes)
	}
	// Set new file metadata back to master
	// if offset+writtenBytes > fileSize {
	// 	fileSize = offset + writtenBytes
	// }
	// argS = util.SetFileMetaArg{
	// 	Path: path,
	// 	Size: fileSize,
	// }
	// err = util.Call(string(c.masterAddr), "Master.SetFileMetaRPC", argS, &retS)
	// if err != nil {
	// 	return
	// }
	return
}

// Write a file.
// should contact the master first, then write the data directly to chunkserver
func (c *Client) Write(w http.ResponseWriter, r *http.Request) {
	var argF util.GetFileMetaArg
	var retF util.GetFileMetaRet
	var argW util.WriteArg

	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&argW)
	if err != nil {
		logrus.Fatalln("Client write failed :", err)
		w.WriteHeader(400)
		return
	}

	// Get the file metadata and check
	path := c.fdTable[argW.Fd]
	if path == "" {
		err = fmt.Errorf("Client write failed : fd %d is not opened\n", argW.Fd)
		return
	}
	argF.Path = path
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", argF, &retF)
	if !retF.Exist {
		logrus.Fatalln("Client write failed :", err)
		return
	}
	// fileSize := retF.Size
	// if argW.Offset > fileSize {
	// 	err = fmt.Errorf("Client write failed : write offset exceed file size\n")
	// 	return
	// }

	// Write to chunk
	// writtenBytes, err := c._Write(path, argW.Offset, argW.Data, fileSize)
	writtenBytes, err := c._Write(path, argW.Offset, argW.Data)
	if err != nil {
		logrus.Fatalln("Client write failed :", err)
		w.WriteHeader(400)
		return
	}
	msg, _ := json.Marshal(writtenBytes)
	w.Write(msg)
	return
}

// GetFileInfo get one file information
func (c *Client) GetFileInfo(w http.ResponseWriter, r *http.Request) {
	var arg util.GetFileMetaArg
	var ret util.GetFileMetaRet
	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		logrus.Fatalln("Client getFileInfo failed :", err)
		w.WriteHeader(400)
		return
	}
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", arg, &ret)
	if err != nil {
		logrus.Fatalln("Client getFileInfo failed :", err)
		w.WriteHeader(400)
		return
	}
	msg, _ := json.Marshal(ret)
	w.Write(msg)
}

func (c *Client) List(w http.ResponseWriter, r *http.Request) {
	var arg util.ListArg
	var ret util.ListRet
	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		logrus.Fatalln("Client getFileInfo failed :", err)
		w.WriteHeader(400)
		return
	}
	err = util.Call(string(c.masterAddr), "Master.ListRPC", arg, &ret)
	msg, _ := json.Marshal(ret)
	w.Write(msg)
}

// Scan scan all files' information in a dir
func (c *Client) Scan(w http.ResponseWriter, r *http.Request) {
	var arg util.ScanArg
	var ret util.ScanRet
	// Decode the params
	err := json.NewDecoder(r.Body).Decode(&arg)
	if err != nil {
		logrus.Fatalln("Client getFileInfo failed :", err)
		w.WriteHeader(400)
		return
	}
	err = util.Call(string(c.masterAddr), "Master.GetFileMetaRPC", arg, &ret)
	msg, _ := json.Marshal(ret)
	w.Write(msg)
}

// helper method for ConcurrentAppend
func (c *Client) _ConcurrentAppend(index int, data []byte) (int, error) {
	return 0, nil
}

// helper method for Append
func (c *Client) _Append(path util.DFSPath, data []byte) (offset int, err error) {
	var argR util.GetReplicasArg
	var retR util.GetReplicasRet
	var argL util.LoadDataArgs
	var retL util.LoadDataReply
	var argC util.SyncArgs
	var retC util.SyncReply

	// Write the chunk (may add chunks)
	// By default, the first entry int retR.ChunkServerAddr is the primary
	argR.Path = path

	//TODO: 1.get the last chunk from master  (handle, address[])
	//2.append data to chunkserver  (load + sync)
	//3.if chunkserver says that chunk does not have enough room to store data, call getNextChunk in master (if have nextChunk,return newChunk;else, create a newChunk,return newChunk)
	//4.redo 2
	argR.ChunkIndex = -1
	for {
		err = util.Call(string(c.masterAddr), "Master.GetReplicasRPC", argR, &retR)
		if err != nil {
			return
		}
		logrus.Debugf(" ChunkHandle : %d Addresses : %s %s %s\n", retR.ChunkHandle, retR.ChunkServerAddrs[0], retR.ChunkServerAddrs[1], retR.ChunkServerAddrs[2])
		// roundWrittenBytes := int(math.Min(float64(util.MAXCHUNKSIZE-(offset+writtenBytes)%util.MAXCHUNKSIZE), float64(len(data)-writtenBytes)))
		var cid = util.CacheID{
			Handle:     retR.ChunkHandle,
			ClientAddr: c.clientAddr,
		}
		argL.CID = cid
		argL.Data = data

		// TODO: make it random , now is fixed order
		argL.Addrs = retR.ChunkServerAddrs
		//argL.Addrs = make([]util.Address,0)
		//for _,index := range rand.Perm(len(retR.ChunkServerAddrs)){
		//	argL.Addrs = append(argL.Addrs,retR.ChunkServerAddrs[index])
		//}
		// Send to Master now
		err = util.Call(string(argL.Addrs[0]), "ChunkServer.LoadDataRPC", argL, &retL)
		if err != nil {
			logrus.Fatalln("Client write failed :", err)
			return
		}
		argC = util.SyncArgs{
			CID:      cid,
			Off:      -1,
			Addrs:    retR.ChunkServerAddrs[1:],
			IsAppend: true,
		}
		err = util.Call(string(argL.Addrs[0]), "ChunkServer.SyncRPC", argC, &retC)
		if err == nil {
			offset = retC.Off
			logrus.Debugf(" Append %d bytes to chunkserver %s, offset %d\n", len(data), argL.Addrs[0], retC.Off)
			return
		} else if retC.ErrorCode != util.NOSPACE {
			//TODO: we should retry append
			logrus.Fatalln("Client write failed :", err)
			break
		}

		// errorcode == nospace, try append to the next chunk
		argR.ChunkIndex = retR.ChunkIndex + 1
	}
	return
}
