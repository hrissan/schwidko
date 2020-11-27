package main

// TODO - lots of tests

/*
	writer := bytes.Buffer{}
	testData := []byte(
		"POST /post_identity_body_world?q=search#hey HTTP/1.1\r\n" +
			"Accept: *\r\n" +
			"Transfer-Encoding: identity\r\n" +
			"  ,chunked\r\n" +
			"Alpha: sta\r\n" +
			" rt\r\n" +
			"Content-Length: 5\r\n" +
			"\r\n" +
			"World")

	c := Client{server: &s,
		conn:           nil,
		incomingBuffer: make([]byte, incomingBufferSize),
		incomingReader: bytes.NewReader(testData),
		outgoingWriter: bufio.NewWriter(&writer),
	}
	err := c.readRequest()
	if err != nil {
		log.Fatalf("Error %v", err)
	}
*/
