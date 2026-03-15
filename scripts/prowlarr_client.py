#!/usr/bin/env python3
"""
Prowlarr Adapter for GoStorm Sync Scripts
Converts Prowlarr/Newznab results to Stremio/Torrentio format.
"""

import json
import logging
import os
import time
import requests
from typing import List, Dict, Any, Optional


def _resolve_config_path(explicit_path: Optional[str] = None) -> str:
    script_dir = os.path.dirname(os.path.abspath(__file__))
    candidates = []

    if explicit_path:
        candidates.append(explicit_path)

    env_path = os.environ.get('MKV_PROXY_CONFIG_PATH')
    if env_path:
        candidates.append(env_path)

    # Common container/install locations.
    candidates.extend([
        '/config/config.json',
        '/app/config.json',
        os.path.join(script_dir, '..', 'config.json'),
    ])

    seen = set()
    for path in candidates:
        if not path or path in seen:
            continue
        seen.add(path)
        if os.path.isfile(path):
            return path

    # Keep previous fallback behavior, but only after trying all known locations.
    return os.path.join(script_dir, '..', 'config.json')

class ProwlarrClient:
    def __init__(self, config_path=None):
        config_path = _resolve_config_path(config_path)

        prowlarr_cfg = {}
        try:
            with open(config_path, 'r') as f:
                cfg = json.load(f)
                prowlarr_cfg = cfg.get('prowlarr', {})
        except FileNotFoundError:
            logging.info(
                "Prowlarr config not found at %s; adapter will remain disabled unless config exists.",
                config_path,
            )
        except Exception as e:
            logging.warning(f"Could not load Prowlarr config from {config_path}: {e}")

        self.ENABLED = prowlarr_cfg.get('enabled', False)
        self.API_KEY = prowlarr_cfg.get('api_key', '')
        self.BASE_URL = prowlarr_cfg.get('url', '')
        self.SEARCH_ENDPOINT = f"{self.BASE_URL}/api/v1/search"
        # Prowlarr searches can be slow when querying many indexers; keep this configurable.
        self.REQUEST_TIMEOUT = int(
            os.environ.get(
                'PROWLARR_HTTP_TIMEOUT_SECONDS',
                prowlarr_cfg.get('timeout_seconds', 90),
            )
        )
        self.MAX_RETRIES = int(os.environ.get('PROWLARR_MAX_RETRIES', '2'))
        self.RETRY_DELAY_SECONDS = float(os.environ.get('PROWLARR_RETRY_DELAY_SECONDS', '1.5'))
        self.session = requests.Session()
        self.session.headers.update({
            'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
            'Accept': 'application/json'
        })
        
    def fetch_from_prowlarr(self, imdb_id: str, content_type: str = "movie") -> List[Dict[str, Any]]:
        """
        Directly query Prowlarr API for a specific IMDB ID.
        """
        if not self.ENABLED:
            return []
            
        # Map Stremio types to Prowlarr search types
        # Movies -> movie, Series -> tvsearch
        prowlarr_type = "tvsearch" if content_type == "series" else "movie"
        
        params = {
            "apikey": self.API_KEY,
            "query": imdb_id,  # Prowlarr V1 uses query for ID searches
            "type": prowlarr_type,
            "indexerIds": "-2"  # All indexers
        }
        for attempt in range(1, self.MAX_RETRIES + 1):
            try:
                response = self.session.get(
                    self.SEARCH_ENDPOINT,
                    params=params,
                    timeout=self.REQUEST_TIMEOUT,
                )
                if response.status_code == 200:
                    return response.json()
                if response.status_code in (401, 403):
                    logging.error(f"Prowlarr auth failed with status {response.status_code}; check api_key")
                    return []
                logging.warning(f"Prowlarr API returned status {response.status_code}")
            except Exception as e:
                if attempt < self.MAX_RETRIES:
                    logging.warning(
                        "Prowlarr request failed (attempt %d/%d): %s; retrying in %.1fs",
                        attempt,
                        self.MAX_RETRIES,
                        e,
                        self.RETRY_DELAY_SECONDS,
                    )
                    time.sleep(self.RETRY_DELAY_SECONDS)
                    continue
                logging.error(f"Error fetching from Prowlarr after {self.MAX_RETRIES} attempts: {e}")
        return []

    def fetch_torrents(self, imdb_id: str, content_type: str = "movie") -> List[Dict[str, Any]]:
        """
        Fetch torrents from Prowlarr and return them in Stremio/Torrentio format.
        """
        if not self.ENABLED:
            return []
            
        prowlarr_results = self.fetch_from_prowlarr(imdb_id, content_type)
        return self._map_to_stremio_format(prowlarr_results)

    def _map_to_stremio_format(self, prowlarr_results: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """
        Maps Prowlarr results to Stremio/Torrentio 'streams' format.
        Fakes Torrentio name to trigger GoStorm quality filters.
        """
        import re
        streams = []
        for res in prowlarr_results:
            title = res.get("title", "")
            size_bytes = res.get("size", 0)
            seeders = res.get("seeders", 0)
            leechers = res.get("leechers", 0)
            info_hash = res.get("infoHash", "")
            
            if not info_hash:
                continue

            # V1.4.6-Fix: Exclude garbage releases (HDTS, WEBSCREENER, etc.)
            if re.search(r'hdts|ts|tc|telecine|telesync|screener|scr|webscreener', title, re.IGNORECASE):
                continue

            # Resolution Mapping (Prowlarr API -> Torrentio Semantics)
            # res_val is numeric: 2160, 1080, 720
            res_val = res.get("quality", {}).get("quality", {}).get("resolution", 0)
            
            if res_val == 2160:
                res_tag = "4k"
            elif res_val == 1080:
                res_tag = "1080p"
            elif res_val == 720:
                res_tag = "720p"
            else:
                # Fallback to regex on title if API resolution is unknown
                if re.search(r'2160p|4k|uhd', title, re.IGNORECASE):
                    res_tag = "4k"
                elif re.search(r'1080p', title, re.IGNORECASE):
                    res_tag = "1080p"
                elif re.search(r'720p', title, re.IGNORECASE):
                    res_tag = "720p"
                else:
                    res_tag = "1080p" # Safe default

            # Convert size to GB for the title string
            size_gb = size_bytes / (1024 * 1024 * 1024)
            
            # Format title to match Torrentio's multiline format (essential for existing regex)
            formatted_title = f"{title}\n👤 {seeders} ⬇️ {leechers}\n💾 {size_gb:.2f}GB"
            
            stream = {
                # CRITICAL: Must start with "Torrentio\n" followed by resolution to trigger filters
                "name": f"Torrentio\n{res_tag}",
                "title": formatted_title,
                "infoHash": info_hash,
                "behaviorHints": {
                    "bingeGroup": f"prowlarr-{res_tag}"
                }
            }
            streams.append(stream)
            
        return streams
