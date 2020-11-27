package main

import "log"

func expectChar(ib []byte, pos *int, c byte) bool {
	if ib[*pos] != c {
		return false
	}
	(*pos)++
	return true
}

func skipSP(ib []byte, pos *int) {
	for ; ; (*pos)++ {
		input := ib[*pos]
		if !isSP(input) {
			break
		}
	}
}

func (c *Client) parseMethod(ib []byte, pos *int) bool {
	start := *pos
	for ; ; (*pos)++ {
		input := ib[*pos]
		if isSP(input) {
			c.request.Method = ib[start:*pos]
			return true
		}
		if isBad(input) {
			return false
		}
	}
}

func parseAnchor(ib []byte, pos *int) bool {
	for ; ; (*pos)++ {
		input := ib[*pos]
		if isSP(input) {
			return true
		}
		if isCTL(input) {
			return false
		}
	}
}

func (c *Client) parseQueryString(ib []byte, pos *int) bool {
	start := *pos
	for ; ; (*pos)++ {
		input := ib[*pos]
		if isSP(input) {
			c.request.QueryString = ib[start:*pos]
			return true
		}
		if input == '#' {
			c.request.Path = ib[start:*pos]
			(*pos)++
			return parseAnchor(ib, pos)
		}
		if isCTL(input) {
			return false
		}
	}
}

func (c *Client) parseURIShifted(ib []byte, pos *int, uriStart int) bool { // pos points at % char
	uriWritePos := *pos
	digit1 := fromHexDigit(ib[*pos+1])
	digit2 := fromHexDigit(ib[*pos+2])
	if digit1 < 0 || digit2 < 0 {
		//c.parseError = "URI percent-encoding invalid first hex digit"
		return false
	}
	(*pos) += 3
	ib[uriWritePos] = byte(digit1*16 + digit2)
	uriWritePos += 1

	for ; ; (*pos)++ {
		input := ib[*pos]
		if isSP(input) {
			c.request.Path = ib[uriStart:uriWritePos]
			return true
		}
		if input == '#' {
			c.request.Path = ib[uriStart:uriWritePos]
			(*pos)++
			return parseAnchor(ib, pos)
		}
		if input == '?' {
			c.request.Path = ib[uriStart:uriWritePos]
			(*pos)++
			return c.parseQueryString(ib, pos)
		}
		if input == '%' {
			digit1 := fromHexDigit(ib[*pos+1])
			digit2 := fromHexDigit(ib[*pos+2])
			if digit1 < 0 || digit2 < 0 {
				//c.parseError = "URI percent-encoding invalid first hex digit"
				return false
			}
			(*pos) += 3
			ib[uriWritePos] = byte(digit1*16 + digit2)
			uriWritePos += 1
		}
		if isCTL(input) {
			return false
		}
	}
}

func (c *Client) parseURI(ib []byte, pos *int) bool {
	uriStart := *pos
	input := ib[*pos]
	if input == '#' {
		//c.parseError = "Invalid '#' character at uri start"
		return false
	}
	if input == '?' {
		//c.parseError = "Invalid '?' character at uri start"
		return false
	}
	if input == '%' {
		return c.parseURIShifted(ib, pos, uriStart)
	}
	if isCTL(input) {
		//c.parseError = "Invalid (control) character at uri start"
		return false
	}
	(*pos)++

	for ; ; (*pos)++ {
		input := ib[*pos]
		if isSP(input) {
			c.request.Path = ib[uriStart:*pos]
			return true
		}
		if input == '#' {
			c.request.Path = ib[uriStart:*pos]
			(*pos)++
			return parseAnchor(ib, pos)
		}
		if input == '?' {
			c.request.Path = ib[uriStart:*pos]
			(*pos)++
			return c.parseQueryString(ib, pos)
		}
		if input == '%' {
			return c.parseURIShifted(ib, pos, uriStart)
		}
		if isCTL(input) {
			return false
		}
	}
}

func (c *Client) parseVersionMinor(ib []byte, pos *int) bool {
	input := ib[*pos]
	*pos++
	if !isDigit(input) {
		return false
	}
	c.request.VersionMinor = int(input) - '0'
	input = ib[*pos]
	if isDigit(input) {
		c.request.VersionMinor = c.request.VersionMinor*10 + int(input) - '0'
		*pos++
	}
	c.request.KeepAlive = c.request.VersionMajor == 1 && c.request.VersionMinor >= 1
	skipSP(ib, pos)
	input = ib[*pos]
	if input == '\r' {
		(*pos)++
		if !expectChar(ib, pos, '\n') {
			return false
		}
		return true
	}
	if input == '\n' {
		(*pos)++
		return true
	}
	return false
}

func (c *Client) parseHeaderKey(ib []byte, pos *int, headerKeyFinish *int) bool {
	for ; ; (*pos)++ {
		input := ib[*pos]
		if input == ':' {
			*headerKeyFinish = *pos
			(*pos)++
			return true
		}
		if isSP(input) {
			return false
		}
		if isBad(input) {
			return false
		}
	}
}

func (c *Client) parseHeaderValue(ib []byte, pos *int, headerValueFinish *int) (bool, bool) {
	for ; ; (*pos)++ {
		input := ib[*pos]
		if input == '\r' {
			*headerValueFinish = *pos
			(*pos)++
			if !expectChar(ib, pos, '\n') {
				return false, false
			}
			return true, false
		}
		if input == '\n' {
			*headerValueFinish = *pos
			(*pos)++
			return true, false
		}
		if c.headerCMSList && input == ',' {
			*headerValueFinish = *pos
			(*pos)++
			return true, true
		}
		if isCTL(input) {
			return false, false
		}
	}
}

func (c *Client) parseHeaderValueShifted(ib []byte, pos *int, headerValueWritePos *int) (bool, bool) {
	for ; ; (*pos)++ {
		input := ib[*pos]
		if input == '\r' {
			(*pos)++
			if !expectChar(ib, pos, '\n') {
				return false, false
			}
			return true, false
		}
		if input == '\n' {
			(*pos)++
			return true, false
		}
		if c.headerCMSList && input == ',' {
			(*pos)++
			return true, true
		}
		if isCTL(input) {
			return false, false
		}
		ib[*headerValueWritePos] = input
		*headerValueWritePos++
	}
}

func (c *Client) parseHeaders(ib []byte, pos *int) bool {
	//headerKeyStart := 0
	//headerKeyFinish := 0
	//headerValueStart := 0
	//headerValueWritePos := 0
	input := ib[*pos]
	if input == '\r' {
		(*pos)++
		if !expectChar(ib, pos, '\n') {
			return false
		}
		return true
	}
	if input == '\n' {
		(*pos)++
		return true
	}
	if isSP(input) { // value continuation on first line
		return false
	}
	headerKeyStart := *pos
	headerKeyFinish := 0
	if !c.parseHeaderKey(ib, pos, &headerKeyFinish) {
		return false
	}
	skipSP(ib, pos)
	headerValueStart := 0
	headerValueWritePos := 0
	for {
		headerValueStart = *pos
		good, cms_cont := c.parseHeaderValue(ib, pos, &headerValueWritePos)
		if !good {
			return false
		}
		if !cms_cont {
			break
		}
		b := !c.processReadyHeader(ib[headerKeyStart:headerKeyFinish], ib[headerValueStart:headerValueWritePos])
		if b {
			return false
		}
	}

	for {
		input := ib[*pos]
		if isSP(input) { // value continuation
			log.Fatal("Value Cont not yet supported")
			/*			ib[headerValueWritePos] = input
						headerValueWritePos++
						for {
							good, cms_cont := c.parseHeaderValueShifted(ib, pos, &headerValueWritePos)
							if !good {
								return false
							}
							if !cms_cont {
								headerValueWritePos = *pos
								break
							}
							b := !c.processReadyHeader(ib[headerKeyStart:headerKeyFinish], ib[headerValueStart:headerValueWritePos])
							if b {
								return false
							}
						}
						continue*/
		}
		if !c.processReadyHeader(ib[headerKeyStart:headerKeyFinish], ib[headerValueStart:headerValueWritePos]) {
			return false
		}
		if input == '\r' {
			(*pos)++
			if !expectChar(ib, pos, '\n') {
				return false
			}
			return true
		}
		if input == '\n' {
			(*pos)++
			return true
		}
		headerKeyStart = *pos
		if !c.parseHeaderKey(ib, pos, &headerKeyFinish) {
			return false
		}

		skipSP(ib, pos)
		for {
			headerValueStart = *pos
			good, cms_cont := c.parseHeaderValue(ib, pos, &headerValueWritePos)
			if !good {
				return false
			}
			if !cms_cont {
				break
			}
			if !c.processReadyHeader(ib[headerKeyStart:headerKeyFinish], ib[headerValueStart:headerValueWritePos]) {
				return false
			}
		}
	}
}

func (c *Client) parse2() string {
	ib := c.incomingBuffer
	pos := c.incomingReadPos
	if ib[pos] == '\r' {
		pos++
		if !expectChar(ib, &pos, '\n') {
			return "error"
		}
	}
	if !c.parseMethod(ib, &pos) {
		return "error"
	}
	skipSP(ib, &pos)
	if !c.parseURI(ib, &pos) {
		return "error"
	}
	skipSP(ib, &pos)
	if ib[pos] != 'H' || ib[pos+1] != 'T' || ib[pos+2] != 'T' || ib[pos+3] != 'P' || ib[pos+4] != '/' || ib[pos+5] != '1' || ib[pos+6] != '.' {
		return "error"
	}
	c.request.VersionMajor = 1
	pos += 7
	if !c.parseVersionMinor(ib, &pos) {
		return "error"
	}
	if !c.parseHeaders(ib, &pos) {
		return "error"
	}
	c.incomingReadPos = pos
	return ""
}
