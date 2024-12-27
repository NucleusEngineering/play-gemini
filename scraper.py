import json
from typing import Any, Dict
from html import unescape
import ssl
import time
from typing import Union
from typing import Any, Callable, List, Optional

from urllib.error import HTTPError
from urllib.request import Request, urlopen
from time import sleep, datetime
from typing import List, Optional, Tuple
from urllib.parse import quote
from enum import Enum
from abc import ABC, abstractmethod
import re
class GooglePlayScraperException(Exception):
    pass


class NotFoundError(GooglePlayScraperException):
    pass


class ExtraHTTPError(GooglePlayScraperException):
    pass
    
    
ssl._create_default_https_context = ssl._create_unverified_context

MAX_RETRIES = 3
RATE_LIMIT_DELAY = 5


def _urlopen(obj):
    try:
        resp = urlopen(obj)
    except HTTPError as e:
        if e.code == 404:
            raise NotFoundError("App not found(404).")
        else:
            raise ExtraHTTPError(
                "App not found. Status code {} returned.".format(e.code)
            )

    return resp.read().decode("UTF-8")


def post(url: str, data: Union[str, bytes], headers: dict) -> str:
    last_exception = None
    rate_exceeded_count = 0
    for _ in range(MAX_RETRIES):
        try:
            resp = _urlopen(Request(url, data=data, headers=headers))
        except Exception as e:
            last_exception = e
            continue
        if "com.google.play.gateway.proto.PlayGatewayError" in resp:
            rate_exceeded_count += 1
            last_exception = Exception("com.google.play.gateway.proto.PlayGatewayError")
            time.sleep(RATE_LIMIT_DELAY * rate_exceeded_count)
            continue
        return resp
    raise last_exception


def get(url: str) -> str:
    return _urlopen(url)
    
def unescape_text(s):
    return unescape(s.replace("<br>", "\r\n"))
    
def nested_lookup(source, indexes):
    try:
        if len(indexes) == 1:
            return source[indexes[0]]

        return nested_lookup(source[indexes[0]], indexes[1:])
    except TypeError:
        return None    

def permissions(app_id: str, lang: str = "en", country: str = "us") -> Dict[str, list]:
    dom = post(
        Formats.Permissions.build(lang=lang, country=country),
        Formats.Permissions.build_body(app_id),
        {"content-type": "application/x-www-form-urlencoded"},
    )

    matches = json.loads(Regex.PERMISSIONS.findall(dom)[0])
    container = json.loads(matches[0][2])

    result = {}

    for permission_items in container:
        if isinstance(permission_items, list):
            if len(permission_items[0]) == 2:
                # rearrange layout to fit ElementSpecs
                permission_items = [["Uncategorized", None, permission_items, None]]

            for permission in permission_items:
                if permission:
                    result[
                        ElementSpecs.PermissionType.extract_content(permission)
                    ] = ElementSpecs.PermissionList.extract_content(permission)

    return result
    

class ElementSpec:
    def __init__(
        self,
        ds_num: Optional[int],
        data_map: List[int],
        post_processor: Callable = None,
        fallback_value: Any = None,
    ):
        self.ds_num = ds_num
        self.data_map = data_map
        self.post_processor = post_processor
        self.fallback_value = fallback_value

    def extract_content(self, source: dict) -> Any:
        try:
            if self.ds_num is None:
                result = nested_lookup(source, self.data_map)
            else:
                result = nested_lookup(
                    source["ds:{}".format(self.ds_num)], self.data_map
                )

            if self.post_processor is not None:
                result = self.post_processor(result)
        except:
            if isinstance(self.fallback_value, ElementSpec):
                result = self.fallback_value.extract_content(source)
            else:
                result = self.fallback_value

        return result


def extract_categories(s, categories=None):
    # Init an empty list if first iteration
    if categories is None:
        categories = []
    if s is None or len(s) == 0:
        return categories

    if len(s) >= 4 and type(s[0]) is str:
        categories.append({"name": s[0], "id": s[2]})
    else:
        for sub in s:
            extract_categories(sub, categories)

    return categories


def get_categories(s):
    categories = extract_categories(nested_lookup(s, [118]))
    if len(categories) == 0:
        # add genre and genreId like GP does when there're no categories available
        categories.append(
            {
                "name": nested_lookup(s, [79, 0, 0, 0]),
                "id": nested_lookup(s, [79, 0, 0, 2]),
            }
        )

    return categories


class ElementSpecs:

    Detail = {
        "title": ElementSpec(5, [1, 2, 0, 0]),
        "description": ElementSpec(
            5,
            [1, 2],
            lambda s: unescape_text(
                nested_lookup(s, [12, 0, 0, 1]) or nested_lookup(s, [72, 0, 1])
            ),
        ),
        "descriptionHTML": ElementSpec(
            5,
            [1, 2],
            lambda s: nested_lookup(s, [12, 0, 0, 1]) or nested_lookup(s, [72, 0, 1]),
        ),
        "summary": ElementSpec(5, [1, 2, 73, 0, 1], unescape_text),
        "installs": ElementSpec(5, [1, 2, 13, 0]),
        "minInstalls": ElementSpec(5, [1, 2, 13, 1]),
        "realInstalls": ElementSpec(5, [1, 2, 13, 2]),
        "score": ElementSpec(5, [1, 2, 51, 0, 1]),
        "ratings": ElementSpec(5, [1, 2, 51, 2, 1]),
        "reviews": ElementSpec(5, [1, 2, 51, 3, 1]),
        "histogram": ElementSpec(
            5,
            [1, 2, 51, 1],
            lambda container: [
                container[1][1],
                container[2][1],
                container[3][1],
                container[4][1],
                container[5][1],
            ],
            [0, 0, 0, 0, 0],
        ),
        "price": ElementSpec(
            5, [1, 2, 57, 0, 0, 0, 0, 1, 0, 0], lambda price: (price / 1000000) or 0
        ),
        "free": ElementSpec(5, [1, 2, 57, 0, 0, 0, 0, 1, 0, 0], lambda s: s == 0),
        "currency": ElementSpec(5, [1, 2, 57, 0, 0, 0, 0, 1, 0, 1]),
        "sale": ElementSpec(4, [0, 2, 0, 0, 0, 14, 0, 0], bool, False),
        "saleTime": ElementSpec(4, [0, 2, 0, 0, 0, 14, 0, 0]),
        "originalPrice": ElementSpec(
            3, [0, 2, 0, 0, 0, 1, 1, 0], lambda price: (price / 1000000) or 0
        ),
        "saleText": ElementSpec(4, [0, 2, 0, 0, 0, 14, 1]),
        "offersIAP": ElementSpec(5, [1, 2, 19, 0], bool, False),
        "inAppProductPrice": ElementSpec(5, [1, 2, 19, 0]),
        # "size": ElementSpec(8, [0]),
        # "androidVersion": ElementSpec(5, [1, 2, 140, 1, 1, 0, 0, 1], lambda s: s.split()[0]),
        # "androidVersionText": ElementSpec(5, [1, 2, 140, 1, 1, 0, 0, 1]),
        "developer": ElementSpec(5, [1, 2, 68, 0]),
        "developerId": ElementSpec(5, [1, 2, 68, 1, 4, 2], lambda s: s.split("id=")[1]),
        "developerEmail": ElementSpec(5, [1, 2, 69, 1, 0]),
        "developerWebsite": ElementSpec(5, [1, 2, 69, 0, 5, 2]),
        "developerAddress": ElementSpec(5, [1, 2, 69, 2, 0]),
        "privacyPolicy": ElementSpec(5, [1, 2, 99, 0, 5, 2]),
        # "developerInternalID": ElementSpec(5, [0, 12, 5, 0, 0]),
        "genre": ElementSpec(5, [1, 2, 79, 0, 0, 0]),
        "genreId": ElementSpec(5, [1, 2, 79, 0, 0, 2]),
        "categories": ElementSpec(5, [1, 2], get_categories, []),
        "icon": ElementSpec(5, [1, 2, 95, 0, 3, 2]),
        "headerImage": ElementSpec(5, [1, 2, 96, 0, 3, 2]),
        "screenshots": ElementSpec(
            5, [1, 2, 78, 0], lambda container: [item[3][2] for item in container], []
        ),
        "video": ElementSpec(5, [1, 2, 100, 0, 0, 3, 2]),
        "videoImage": ElementSpec(5, [1, 2, 100, 1, 0, 3, 2]),
        "contentRating": ElementSpec(5, [1, 2, 9, 0]),
        "contentRatingDescription": ElementSpec(5, [1, 2, 9, 2, 1]),
        "adSupported": ElementSpec(5, [1, 2, 48], bool),
        "containsAds": ElementSpec(5, [1, 2, 48], bool, False),
        "released": ElementSpec(5, [1, 2, 10, 0]),
        "lastUpdatedOn": ElementSpec(5, [1, 2, 145, 0, 0]),
        "updated": ElementSpec(5, [1, 2, 145, 0, 1, 0]),
        "version": ElementSpec(
            5, [1, 2, 140, 0, 0, 0], fallback_value="Varies with device"
        ),
        # "recentChanges": ElementSpec(5, [1, 2, 144, 1, 1], unescape_text),
        # "recentChangesHTML": ElementSpec(5, [1, 2, 144, 1, 1]),
        "comments": ElementSpec(
            8, [0], lambda container: [item[4] for item in container], []
        ),
        # "editorsChoice": ElementSpec(5, [0, 12, 15, 0], bool, False),
        # "similarApps": ElementSpec(
        #     7,
        #     [1, 1, 0, 0, 0],
        #     lambda container: [container[i][12][0] for i in range(0, len(container))],
        # ),
        # "moreByDeveloper": [
        #     ElementSpec(
        #         9,
        #         [0, 1, 0, 0, 0],
        #         lambda container: [
        #             container[i][12][0] for i in range(0, len(container))
        #         ],
        #     ),
        #     ElementSpec(
        #         9,
        #         [0, 1, 0, 6, 0],
        #         lambda container: [
        #             container[i][12][0] for i in range(0, len(container))
        #         ],
        #     ),
        # ],
    }
    Review = {
        "reviewId": ElementSpec(None, [0]),
        "userName": ElementSpec(None, [1, 0]),
        "userImage": ElementSpec(None, [1, 1, 3, 2]),
        "content": ElementSpec(None, [4]),
        "score": ElementSpec(None, [2]),
        "thumbsUpCount": ElementSpec(None, [6]),
        "reviewCreatedVersion": ElementSpec(None, [10]),
        "at": ElementSpec(None, [5, 0], lambda v: datetime.fromtimestamp(v)),
        "replyContent": ElementSpec(None, [7, 1]),
        "repliedAt": ElementSpec(None, [7, 2, 0], lambda v: datetime.fromtimestamp(v)),
        "appVersion": ElementSpec(None, [10]),
    }

    PermissionType = ElementSpec(None, [0])

    PermissionList = ElementSpec(
        None, [2], lambda container: sorted([item[1] for item in container])
    )

    SearchResultOnTop = {
        "appId": ElementSpec(None, [11, 0, 0]),
        "icon": ElementSpec(None, [2, 95, 0, 3, 2]),
        "screenshots": ElementSpec(
            None,
            [2, 78, 0],
            lambda container: [item[3][2] for item in container],
            [],
        ),
        "title": ElementSpec(None, [2, 0, 0]),
        "score": ElementSpec(None, [2, 51, 0, 1]),
        "genre": ElementSpec(None, [2, 79, 0, 0, 0]),
        "price": ElementSpec(
            None, [2, 57, 0, 0, 0, 0, 1, 0, 0], lambda price: (price / 1000000) or 0
        ),
        "free": ElementSpec(None, [2, 57, 0, 0, 0, 0, 1, 0, 0], lambda s: s == 0),
        "currency": ElementSpec(None, [2, 57, 0, 0, 0, 0, 1, 0, 1]),
        "video": ElementSpec(None, [2, 100, 0, 0, 3, 2]),
        "videoImage": ElementSpec(None, [2, 100, 1, 0, 3, 2]),
        "description": ElementSpec(None, [2, 72, 0, 1], unescape_text),
        "descriptionHTML": ElementSpec(None, [2, 72, 0, 1]),
        "developer": ElementSpec(None, [2, 68, 0]),
        "installs": ElementSpec(None, [2, 13, 0]),
    }

    SearchResult = {
        "appId": ElementSpec(None, [0, 0, 0]),
        "icon": ElementSpec(None, [0, 1, 3, 2]),
        "screenshots": ElementSpec(
            None, [0, 2], lambda container: [item[3][2] for item in container], []
        ),
        "title": ElementSpec(None, [0, 3]),
        "score": ElementSpec(None, [0, 4, 1]),
        "genre": ElementSpec(None, [0, 5]),
        "price": ElementSpec(
            None, [0, 8, 1, 0, 0], lambda price: (price / 1000000) or 0
        ),
        "free": ElementSpec(None, [0, 8, 1, 0, 0], lambda s: s == 0),
        "currency": ElementSpec(None, [0, 8, 1, 0, 1]),
        "video": ElementSpec(None, [0, 12, 0, 0, 3, 2]),
        "videoImage": ElementSpec(None, [0, 12, 0, 3, 3, 2]),
        "description": ElementSpec(None, [0, 13, 1], unescape_text),
        "descriptionHTML": ElementSpec(None, [0, 13, 1]),
        "developer": ElementSpec(None, [0, 14]),
        "installs": ElementSpec(None, [0, 15]),
    }
    

class Sort(int, Enum):
    MOST_RELEVANT = 1
    NEWEST = 2
    RATING = 3


class Device(int, Enum):
    MOBILE = 2
    TABLET = 3
    CHROMEBOOK = 5
    TV = 6    
    
class Regex:
    NOT_NUMBER = re.compile(r"\D")
    SCRIPT = re.compile(r"AF_initDataCallback[\s\S]*?</script")
    KEY = re.compile("(ds:.*?)'")
    VALUE = re.compile(r"data:([\s\S]*?), sideChannel: {}}\);<\/")
    REVIEWS = re.compile(r"\)]}'\n\n([\s\S]+)")
    PERMISSIONS = re.compile(r"\)]}'\n\n([\s\S]+)")    
    
PLAY_STORE_BASE_URL = "https://play.google.com"


class Format(ABC):
    @abstractmethod
    def build(self, *args):
        raise NotImplementedError

    @abstractmethod
    def build_body(self, *args):
        raise NotImplementedError


class Formats:
    class _Detail(Format):
        URL_FORMAT = (
            "{}/store/apps/details?id={{app_id}}&hl={{lang}}&gl={{country}}".format(
                PLAY_STORE_BASE_URL
            )
        )
        FALLBACK_URL_FORMAT = "{}/store/apps/details?id={{app_id}}&hl={{lang}}".format(
            PLAY_STORE_BASE_URL
        )

        def build(self, app_id: str, lang: str, country: str) -> str:
            return self.URL_FORMAT.format(app_id=app_id, lang=lang, country=country)

        def fallback_build(self, app_id: str, lang: str) -> str:
            return self.FALLBACK_URL_FORMAT.format(app_id=app_id, lang=lang)

        def build_body(self, *args):
            return None

    class _Reviews(Format):
        URL_FORMAT = (
            "{}/_/PlayStoreUi/data/batchexecute?hl={{lang}}&gl={{country}}".format(
                PLAY_STORE_BASE_URL
            )
        )

        def build(self, lang: str, country: str) -> str:
            return self.URL_FORMAT.format(lang=lang, country=country)

        PAYLOAD_FORMAT_FOR_FIRST_PAGE = "f.req=%5B%5B%5B%22oCPfdb%22%2C%22%5Bnull%2C%5B2%2C{sort}%2C%5B{count}%5D%2Cnull%2C%5Bnull%2C{score}%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2C{device_id}%5D%5D%2C%5B%5C%22{app_id}%5C%22%2C7%5D%5D%22%2Cnull%2C%22generic%22%5D%5D%5D%0A"
        PAYLOAD_FORMAT_FOR_PAGINATED_PAGE = "f.req=%5B%5B%5B%22oCPfdb%22%2C%22%5Bnull%2C%5B2%2C{sort}%2C%5B{count}%2Cnull%2C%5C%22{pagination_token}%5C%22%5D%2Cnull%2C%5Bnull%2C{score}%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2Cnull%2C{device_id}%5D%5D%2C%5B%5C%22{app_id}%5C%22%2C7%5D%5D%22%2Cnull%2C%22generic%22%5D%5D%5D%0A"

        def build_body(
            self,
            app_id: str,
            sort: int,
            count: int,
            filter_score_with: int,
            filter_device_with: int,
            pagination_token: str,
        ) -> bytes:
            if pagination_token is not None:
                result = self.PAYLOAD_FORMAT_FOR_PAGINATED_PAGE.format(
                    app_id=app_id,
                    sort=sort,
                    count=count,
                    score=filter_score_with,
                    device_id=filter_device_with,
                    pagination_token=pagination_token,
                )
            else:
                result = self.PAYLOAD_FORMAT_FOR_FIRST_PAGE.format(
                    app_id=app_id,
                    sort=sort,
                    count=count,
                    score=filter_score_with,
                    device_id=filter_device_with,
                )
            return result.encode()

    class _Permissions(Format):
        URL_FORMAT = (
            "{}/_/PlayStoreUi/data/batchexecute?hl={{lang}}&gl={{country}}".format(
                PLAY_STORE_BASE_URL
            )
        )

        def build(self, lang: str, country: str) -> str:
            return self.URL_FORMAT.format(lang=lang, country=country)

        PAYLOAD_FORMAT_FOR_PERMISSION = "f.req=%5B%5B%5B%22xdSrCf%22%2C%22%5B%5Bnull%2C%5B%5C%22{app_id}%5C%22%2C7%5D%2C%5B%5D%5D%5D%22%2Cnull%2C%221%22%5D%5D%5D"

        def build_body(self, app_id: str) -> bytes:
            result = self.PAYLOAD_FORMAT_FOR_PERMISSION.format(app_id=app_id)

            return result.encode()

    class _Searchresults(Format):
        URL_FORMAT = (
            "{}/store/search?q={{query}}&c=apps&hl={{lang}}&gl={{country}}".format(
                PLAY_STORE_BASE_URL
            )
        )
        FALLBACK_URL_FORMAT = "{}/store/search?q={{query}}&c=apps&hl={{lang}}".format(
            PLAY_STORE_BASE_URL
        )

        def build(self, query: str, lang: str, country: str) -> str:
            return self.URL_FORMAT.format(query=query, lang=lang, country=country)

        def fallback_build(self, query: str, lang: str) -> str:
            return self.FALLBACK_URL_FORMAT.format(query=query, lang=lang)

        def build_body(self, *args):
            return None

    Detail = _Detail()
    Reviews = _Reviews()
    Permissions = _Permissions()
    Searchresults = _Searchresults()    

class _ContinuationToken:
    __slots__ = (
        "token",
        "lang",
        "country",
        "sort",
        "count",
        "filter_score_with",
        "filter_device_with",
    )

    def __init__(
        self, token, lang, country, sort, count, filter_score_with, filter_device_with
    ):
        self.token = token
        self.lang = lang
        self.country = country
        self.sort = sort
        self.count = count
        self.filter_score_with = filter_score_with
        self.filter_device_with = filter_device_with


def _fetch_review_items(
    url: str,
    app_id: str,
    sort: int,
    count: int,
    filter_score_with: Optional[int],
    filter_device_with: Optional[int],
    pagination_token: Optional[str],
):
    dom = post(
        url,
        Formats.Reviews.build_body(
            app_id,
            sort,
            count,
            "null" if filter_score_with is None else filter_score_with,
            "null" if filter_device_with is None else filter_device_with,
            pagination_token,
        ),
        {"content-type": "application/x-www-form-urlencoded"},
    )
    match = json.loads(Regex.REVIEWS.findall(dom)[0])
    try:
        token = json.loads(match[0][2])[-2][-1]
    except:
        token = None

    results = json.loads(match[0][2])
    if len(results) == 0 or len(results[0]) == 0:
        return [], token
    return results[0], token


def reviews(
    app_id: str,
    lang: str = "en",
    country: str = "us",
    sort: Sort = Sort.NEWEST,
    count: int = 100,
    filter_score_with: int = None,
    filter_device_with: int = None,
    continuation_token: _ContinuationToken = None,
) -> Tuple[List[dict], _ContinuationToken]:
    sort = sort.value

    if continuation_token is not None:
        token = continuation_token.token

        if token is None:
            return (
                [],
                continuation_token,
            )

        lang = continuation_token.lang
        country = continuation_token.country
        sort = continuation_token.sort
        count = continuation_token.count
        filter_score_with = continuation_token.filter_score_with
        filter_device_with = continuation_token.filter_device_with
    else:
        token = None

    url = Formats.Reviews.build(lang=lang, country=country)

    _fetch_count = count

    result = []

    while True:
        if _fetch_count == 0:
            break

        if _fetch_count > MAX_COUNT_EACH_FETCH:
            _fetch_count = MAX_COUNT_EACH_FETCH

        try:
            review_items, token = _fetch_review_items(
                url,
                app_id,
                sort,
                _fetch_count,
                filter_score_with,
                filter_device_with,
                token,
            )
        except Exception:
            token = None
            break

        for review in review_items:
            result.append(
                {
                    k: spec.extract_content(review)
                    for k, spec in ElementSpecs.Review.items()
                }
            )

        _fetch_count = count - len(result)

        if isinstance(token, list):
            token = None
            break
        if token is None:
            break

    return (
        result,
        _ContinuationToken(
            token, lang, country, sort, count, filter_score_with, filter_device_with
        ),
    )


def reviews_all(app_id: str, sleep_milliseconds: int = 0, **kwargs) -> list:
    kwargs.pop("count", None)
    kwargs.pop("continuation_token", None)

    continuation_token = None

    result = []

    while True:
        _result, continuation_token = reviews(
            app_id,
            count=MAX_COUNT_EACH_FETCH,
            continuation_token=continuation_token,
            **kwargs
        )

        result += _result

        if continuation_token.token is None:
            break

        if sleep_milliseconds:
            sleep(sleep_milliseconds / 1000)

    return result    
    
def search(
    query: str, n_hits: int = 30, lang: str = "en", country: str = "us"
) -> List[Dict[str, Any]]:
    if n_hits <= 0:
        return []

    query = quote(query)
    url = Formats.Searchresults.build(query=query, lang=lang, country=country)
    try:
        dom = get(url)
    except NotFoundError:
        url = Formats.Searchresults.fallback_build(query=query, lang=lang)
        dom = get(url)

    matches = Regex.SCRIPT.findall(dom)  # take out script blocks from dom

    dataset = {}

    for match in matches:
        key_match = Regex.KEY.findall(match)
        value_match = Regex.VALUE.findall(match)

        if key_match and value_match:
            key = key_match[0]
            value = json.loads(value_match[0])

            dataset[key] = value

    try:
        top_result = dataset["ds:4"][0][1][0][23][16]
    except IndexError:
        top_result = None

    success = False
    # different idx for different countries and languages
    for idx in range(len(dataset["ds:4"][0][1])):
        try:
            dataset = dataset["ds:4"][0][1][idx][22][0]
            success = True
        except Exception:
            pass
    if not success:
        return []

    n_apps = min(len(dataset), n_hits)

    search_results = (
        [
            {
                k: spec.extract_content(top_result)
                for k, spec in ElementSpecs.SearchResultOnTop.items()
            }
        ]
        if top_result
        else []
    )

    for app_idx in range(n_apps - len(search_results)):
        app = {}
        for k, spec in ElementSpecs.SearchResult.items():
            content = spec.extract_content(dataset[app_idx])
            app[k] = content

        search_results.append(app)

    return search_results    
    
def app(app_id: str, lang: str = "en", country: str = "us") -> Dict[str, Any]:
    url = Formats.Detail.build(app_id=app_id, lang=lang, country=country)

    try:
        dom = get(url)
    except NotFoundError:
        url = Formats.Detail.fallback_build(app_id=app_id, lang=lang)
        dom = get(url)
    return parse_dom(dom=dom, app_id=app_id, url=url)


def parse_dom(dom: str, app_id: str, url: str) -> Dict[str, Any]:
    matches = Regex.SCRIPT.findall(dom)

    dataset = {}

    for match in matches:
        key_match = Regex.KEY.findall(match)
        value_match = Regex.VALUE.findall(match)

        if key_match and value_match:
            key = key_match[0]
            value = json.loads(value_match[0])

            dataset[key] = value

    result = {}

    for k, spec in ElementSpecs.Detail.items():
        content = spec.extract_content(dataset)
        if content is None:
            result[k] = spec.fallback_value
        else:
            result[k] = content

    result["appId"] = app_id
    result["url"] = url

    return result

