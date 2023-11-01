# `calls-transcriber`

A headless speech transcriber for [Mattermost Calls](https://github.com/mattermost/mattermost-plugin-calls).

## Usage

This program is **not** meant to be run directly (nor manually) other than for development/testing purposes. In fact, this is automatically used by the [`calls-offloader`](https://github.com/mattermost/calls-offloader) service to run transcribing jobs. Please refer to that project if you are looking to enable call transcriptions in your Mattermost instance.

## Manual execution (testing only)

### Fetch the latest image

The latest official docker image can be found at https://hub.docker.com/r/mattermost/calls-transcriber.

```
docker pull mattermost/calls-transcriber:latest
```

### Run the container

```
docker run --network=host --name calls-transcriber -e "SITE_URL=http://127.0.0.1:8065/" -e "AUTH_TOKEN=ohqd1phqtt8m3gsfg8j5ymymqy" -e "CALL_ID=9c86b3q57fgfpqr8jq3b9yjweh" -e "POST_ID=e4pdmi6rqpn7pp9sity9hiza3r" -e "DEV_MODE=true" -v calls-transcriber-volume:/recs mattermost/calls-transcriber
```

> **_Note_** 
>
> This process requires:
>  - Mattermost Server >= v7.8
>  - Mattermost Calls >= v0.19.0

> **_Note_**
> - `SITE_URL`: The URL pointing to the Mattermost installation.
> - `AUTH_TOKEN`: The authentication token for the Calls bot.
> - `CALL_ID`: The channel ID in which the call to transcribe has been started.
> - `POST_ID`: The post ID the transcription file(s) should be attached to.

> **_Note_**
>
> The auth token for the bot can be found through this SQL query:
> ```sql
> SELECT Token FROM Sessions JOIN Bots ON Sessions.UserId = Bots.UserId AND Bots.OwnerId = 'com.mattermost.calls' ORDER BY Sessions.CreateAt DESC LIMIT 1;
> ```

### Development

Run `make help` to see available options.
