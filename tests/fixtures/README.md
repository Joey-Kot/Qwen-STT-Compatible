# Public speech fixture

`jfk.flac` is a short excerpt of President John F. Kennedy's inaugural address
(20 January 1961), obtained from the OpenAI Whisper test suite at commit
`86098128c0b4f24f0e2aa2994de830614b474227`:

https://github.com/openai/whisper/blob/86098128c0b4f24f0e2aa2994de830614b474227/tests/jfk.flac

SHA-256: `63a4b1e4c1dc655ac70961ffbf518acd249df237e5a0152faae9a4a836949715`.

The JFK Library identifies the original government recording as public domain:
https://www.jfklibrary.org/learn/about-jfk/historic-speeches/inaugural-address

The upstream repository's MIT license is retained as `Whisper-LICENSE`.
All additional PCM fixtures are generated deterministically during tests.
Private, locally supplied recordings are not test fixtures and are excluded
from Cargo packages and CI.
