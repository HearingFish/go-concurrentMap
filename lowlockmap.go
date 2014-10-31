package concurrent

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	DELETED *LowLockingEntry = new(LowLockingEntry)
)

//segments is read-only, don't need synchronized
type LowLockingMap struct {
	/**
	 * The number of elements in HashMap,
	 * must use atomic Load/Store to read/write this field.
	 * It is a hot spot of date race, but adding this field will simplify IsEmpty and Size method
	 */
	count int32

	/**
	 * Mask value for indexing into segments. The upper bits of a
	 * key's hash code are used to choose the segment.
	 */
	segmentMask int

	/**
	 * Shift value for indexing within segments.
	 */
	segmentShift uint

	/**
	 * The segments, each of which is a specialized hash table
	 */
	segments []*LowLockingSegment
}

/**
 * Returns the segment that should be used for key with given hash
 * @param hash the hash code for the key
 * @return the segment
 */
func (this *LowLockingMap) segmentFor(hash uint32) *LowLockingSegment {
	//默认segmentShift是28，segmentMask是（0xFFFFFFF）,hash>>this.segmentShift就是取前面4位
	//&segmentMask似乎没有必要
	//get first four bytes
	return this.segments[(hash>>this.segmentShift)&uint32(this.segmentMask)]
}

/**
 * Returns true if this map contains no key-value mappings.
 */
func (this *LowLockingMap) IsEmpty() bool {
	return atomic.LoadInt32(&this.count) > 0
}

/**
 * Returns the number of key-value mappings in this map.
 */
func (this *LowLockingMap) Size() int32 {
	return atomic.LoadInt32(&this.count)
}

/**
 * Returns the value to which the specified key is mapped,
 * or nil if this map contains no mapping for the key.
 */
func (this *LowLockingMap) Get(key interface{}) (value interface{}, err error) {
	if isNil(key) {
		return nil, NilKeyError
	}
	hash := hash2(hashI(key))
	value = this.segmentFor(hash).get(key, hash)
	return
}

/**
 * Tests if the specified object is a key in this table.
 *
 * @param  key   possible key
 * @return true if and only if the specified object is a key in this table,
 * as determined by the == method; false otherwise.
 */
func (this *LowLockingMap) ContainsKey(key interface{}) (found bool, err error) {
	if isNil(key) {
		return false, NilKeyError
	}
	hash := hash2(hashI(key))
	found = this.segmentFor(hash).containsKey(key, hash)
	return
}

/**
 * Maps the specified key to the specified value in this table.
 * Neither the key nor the value can be nil.
 *
 * The value can be retrieved by calling the get method
 * with a key that is equal to the original key.
 *
 * @param key with which the specified value is to be associated
 * @param value to be associated with the specified key
 *
 * @return the previous value associated with key, or
 *         nil if there was no mapping for key
 */
func (this *LowLockingMap) Put(key interface{}, value interface{}) (previous interface{}, err error) {
	if isNil(key) {
		return nil, NilKeyError
	}
	if isNil(value) {
		return nil, NilValueError
	}
	hash := hash2(hashI(key))
	previous = this.segmentFor(hash).put(key, hash, value, false)
	return
}

/**
 * If mapping exists for the key, then maps the specified key to the specified value in this table.
 * else will ignore.
 * Neither the key nor the value can be nil.
 *
 * The value can be retrieved by calling the get method
 * with a key that is equal to the original key.
 *
 * @return the previous value associated with the specified key,
 *         or nil if there was no mapping for the key
 */
func (this *LowLockingMap) PutIfAbsent(key interface{}, value interface{}) (previous interface{}, err error) {
	if isNil(key) {
		return nil, NilKeyError
	}
	if isNil(value) {
		return nil, NilValueError
	}
	hash := hash2(hashI(key))
	previous = this.segmentFor(hash).put(key, hash, value, true)
	return
}

/**
 * Copies all of the mappings from the specified map to this one.
 * These mappings replace any mappings that this map had for any of the
 * keys currently in the specified map.
 *
 * @param m mappings to be stored in this map
 */
func (this *LowLockingMap) PutAll(m map[interface{}]interface{}) (err error) {
	if isNil(m) {
		err = errors.New("Cannot copy nil map")
	}
	for k, v := range m {
		this.Put(k, v)
	}
	return
}

/**
 * Removes the key (and its corresponding value) from this map.
 * This method does nothing if the key is not in the map.
 *
 * @param  key the key that needs to be removed
 * @return the previous value associated with key, or nil if there was no mapping for key
 */
func (this *LowLockingMap) Remove(key interface{}) (previous interface{}, err error) {
	if isNil(key) {
		return nil, NilKeyError
	}
	hash := hash2(hashI(key))
	previous = this.segmentFor(hash).remove(key, hash, nil)
	return
}

/**
 * Removes the mapping for the key and value from this map.
 * This method does nothing if no mapping for the key and value.
 *
 * @return true if mapping be removed, false otherwise
 */
func (this *LowLockingMap) RemoveEntry(key interface{}, value interface{}) (ok bool, err error) {
	if isNil(key) {
		return false, NilKeyError
	}
	if isNil(value) {
		return false, NilValueError
	}
	hash := hash2(hashI(key))
	ok = this.segmentFor(hash).remove(key, hash, value) != nil
	return
}

/**
 * CompareAndReplace executes the compare-and-replace operation.
 * Replaces the value if the mapping exists for the oldValue and value from this map.
 * This method does nothing if no mapping for the key and value.
 *
 * @return true if value be replaced, false otherwise
 */
func (this *LowLockingMap) CompareAndReplace(key interface{}, oldValue interface{}, newValue interface{}) (ok bool, err error) {
	if isNil(key) {
		return false, NilKeyError
	}
	if isNil(oldValue) || isNil(newValue) {
		return false, NilValueError
	}
	hash := hash2(hashI(key))
	ok = this.segmentFor(hash).replaceWithOld(key, hash, oldValue, newValue)
	return
}

/**
 * Replaces the value if the key is in the map.
 * This method does nothing if no mapping for the key.
 *
 * @return the previous value associated with the specified key,
 *         or nil if there was no mapping for the key
 */
func (this *LowLockingMap) Replace(key interface{}, value interface{}) (previous interface{}, err error) {
	if isNil(key) {
		return nil, NilKeyError
	}
	if isNil(value) {
		return nil, NilValueError
	}
	hash := hash2(hashI(key))
	previous = this.segmentFor(hash).replace(key, hash, value)
	return
}

/**
 * Removes all of the mappings from this map.
 */
func (this *LowLockingMap) Clear() {
	for i := 0; i < len(this.segments); i++ {
		this.segments[i].clear()
	}
}

//Iterator returns a iterator for ConcurrentMap
func (this *LowLockingMap) Iterator() *LowLockingMapIterator {
	return NewLowLockingMapIterator(this)
}

func newLowLockingMap3(initialCapacity int,
	loadFactor float32, concurrencyLevel int) (m *LowLockingMap) {
	m = &LowLockingMap{}

	if !(loadFactor > 0) || initialCapacity < 0 || concurrencyLevel <= 0 {
		panic(IllegalArgError)
	}

	if concurrencyLevel > MAX_SEGMENTS {
		concurrencyLevel = MAX_SEGMENTS
	}

	// Find power-of-two sizes best matching arguments
	sshift := 0
	ssize := 1
	for ssize < concurrencyLevel {
		sshift++
		ssize = ssize << 1
	}

	m.segmentShift = uint(32) - uint(sshift)
	m.segmentMask = ssize - 1

	m.segments = make([]*LowLockingSegment, ssize)

	if initialCapacity > MAXIMUM_CAPACITY {
		initialCapacity = MAXIMUM_CAPACITY
	}

	c := initialCapacity / ssize
	if c*ssize < initialCapacity {
		c++
	}
	cap := 1
	for cap < c {
		cap <<= 1
	}

	for i := 0; i < len(m.segments); i++ {
		m.segments[i] = m.newSegment(cap, loadFactor)
	}
	return
}

/**
 * Creates a new, empty map with the specified initial
 * capacity, load factor and concurrency level.
 *
 * @param initialCapacity the initial capacity. The implementation
 * performs internal sizing to accommodate this many elements.
 *
 * @param loadFactor  the load factor threshold, used to control resizing.
 * Resizing may be performed when the average number of elements per
 * bin exceeds this threshold.
 *
 * @param concurrencyLevel the estimated number of concurrently
 * updating threads. The implementation performs internal sizing
 * to try to accommodate this many threads.
 *
 * panic error "IllegalArgumentException" if the initial capacity is
 * negative or the load factor or concurrencyLevel are
 * nonpositive.
 *
 * Creates a new, empty map with a default initial capacity (16),
 * load factor (0.75) and concurrencyLevel (16).
 */
func NewLowLockingMap(paras ...interface{}) (m *LowLockingMap) {
	ok := false
	cap := DEFAULT_INITIAL_CAPACITY
	factor := DEFAULT_LOAD_FACTOR
	concurrent_lvl := DEFAULT_CONCURRENCY_LEVEL

	if len(paras) >= 1 {
		if cap, ok = paras[0].(int); !ok {
			panic(IllegalArgError)
		}
	}

	if len(paras) >= 2 {
		if factor, ok = paras[1].(float32); !ok {
			panic(IllegalArgError)
		}
	}

	if len(paras) >= 3 {
		if concurrent_lvl, ok = paras[2].(int); !ok {
			panic(IllegalArgError)
		}
	}

	m = newLowLockingMap3(cap, factor, concurrent_lvl)
	return
}

/**
 * Creates a new map with the same mappings as the given map.
 * The map is created with a capacity of 1.5 times the number
 * of mappings in the given map or 16 (whichever is greater),
 * and a default load factor (0.75) and concurrencyLevel (16).
 *
 * @param m the map
 */
func NewLowLockingMapFromMap(m map[interface{}]interface{}) *LowLockingMap {
	cm := newLowLockingMap3(int(math.Max(float64(float32(len(m))/DEFAULT_LOAD_FACTOR+1),
		float64(DEFAULT_INITIAL_CAPACITY))),
		DEFAULT_LOAD_FACTOR, DEFAULT_CONCURRENCY_LEVEL)
	cm.PutAll(m)
	return cm
}

/**
 * ConcurrentHashMap list entry.
 * Note only value field is variable and must use atomic to read/write it, other three fields are read-only after initializing.
 * so can use unsynchronized reader, the Segment.readValueUnderLock method is used as a
 * backup in case a nil (pre-initialized) value is ever seen in
 * an unsynchronized access method.
 */
type LowLockingEntry struct {
	key   interface{}
	hash  uint32
	value unsafe.Pointer
	next  *LowLockingEntry
}

func (this *LowLockingEntry) Key() interface{} {
	return this.key
}

func (this *LowLockingEntry) Value() interface{} {
	return *((*interface{})(atomic.LoadPointer(&this.value)))
}

func (this *LowLockingEntry) fastValue() interface{} {
	return *((*interface{})(this.value))
}

func (this *LowLockingEntry) storeValue(v *interface{}) {
	atomic.StorePointer(&this.value, unsafe.Pointer(v))
}

type LowLockingSegment struct {
	/**
	 * The pointer that points to HashMap count
	 */
	sumCount *int32

	/**
	 * The number of elements in this segment's region.
	 * Must use atomic package's LoadInt32 and StoreInt32 functions to read/write this field
	 * otherwise read operation may cannot read latest value
	 */
	count int32

	/**
	 * The table is rehashed when its size exceeds this threshold.
	 * (The value of this field is always (int)(capacity *
	 * loadFactor).)
	 */
	threshold int32

	/**
	 * The per-segment table.
	 * Use unsafe.Pointer because must use atomic.LoadPointer function in read operations.
	 */
	pTable unsafe.Pointer //point to []unsafe.Pointer

	/**
	 * The load factor for the hash table. Even though this value
	 * is same for all segments, it is replicated to avoid needing
	 * links to outer object.
	 */
	loadFactor float32

	lock *sync.Mutex
}

func (this *LowLockingSegment) rehash() {
	oldTable := this.table() //*(*[]*Entry)(this.table)
	oldCapacity := len(oldTable)
	if oldCapacity >= MAXIMUM_CAPACITY {
		return
	}

	newTable := make([]unsafe.Pointer, oldCapacity<<1)
	atomic.StoreInt32(&this.threshold, int32(float32(len(newTable))*this.loadFactor))
	for i := 0; i < oldCapacity; i++ {
		// We need to guarantee that any existing reads of old Map can
		//  proceed. So we cannot yet nil out each bin.
		e := (*LowLockingEntry)(oldTable[i])

		if e != nil {
			put(newTable, e, true)
		}
	}
	atomic.StorePointer(&this.pTable, unsafe.Pointer(&newTable))
}

/**
 * Sets table to new pointer slice that all item points to HashEntry.
 * Call only while holding lock or in constructor.
 */
func (this *LowLockingSegment) setTable(newTable []unsafe.Pointer) {
	this.threshold = (int32)(float32(len(newTable)) * this.loadFactor)
	this.pTable = unsafe.Pointer(&newTable)
}

/**
 * uses atomic to load table and returns.
 * Call while no lock.
 */
func (this *LowLockingSegment) loadTable() (table []unsafe.Pointer) {
	return *(*[]unsafe.Pointer)(atomic.LoadPointer(&this.pTable))
}

/**
 * returns pointer slice that all item points to HashEntry.
 * Call only while holding lock or in constructor.
 */
func (this *LowLockingSegment) table() []unsafe.Pointer {
	return *(*[]unsafe.Pointer)(this.pTable)
}

/**
 * Returns properly casted first entry of bin for given hash.
 */
func (this *LowLockingSegment) getFirst(hash uint32) *Entry {
	tab := this.loadTable()
	return (*Entry)(atomic.LoadPointer(&tab[hash&uint32(len(tab)-1)]))
}

/**
 * Reads value field of an entry under lock. Called if value
 * field ever appears to be nil. see below code:
 * 		tab[index] = unsafe.Pointer(&Entry{key, hash, unsafe.Pointer(&value), first})
 * go memory model don't explain Entry initialization must be executed before
 * table assignment. So value is nil is possible only if a
 * compiler happens to reorder a HashEntry initialization with
 * its table assignment, which is legal under memory model
 * but is not known to ever occur.
 */
func (this *LowLockingSegment) readValueUnderLock(e *Entry) interface{} {
	this.lock.Lock()
	defer this.lock.Unlock()
	return e.fastValue()
}

/* Specialized implementations of map methods */

func (this *LowLockingSegment) get(key interface{}, hash uint32) interface{} {
	if atomic.LoadInt32(&this.count) != 0 { // atomic-read
		tab := this.loadTable()
		lenT := uint32(len(tab) - 1)
		idx := hash & lenT
		//这里不会死循环，因为如果Table太满会触发rehash
		for {
			e := (*LowLockingEntry)(atomic.LoadPointer(&tab[idx]))
			if e == nil {
				return nil
			} else if e == DELETED {
				continue
			} else if e.key == key {
				return e.Value()
			}
			idx = (idx + 1) & lenT
		}
	}
	return nil
}

func (this *LowLockingSegment) containsKey(key interface{}, hash uint32) bool {
	if atomic.LoadInt32(&this.count) != 0 { // read-volatile
		tab := this.loadTable()
		lenT := uint32(len(tab) - 1)
		idx := hash & lenT
		//这里不会死循环，因为如果Table太满会触发rehash
		for {
			e := (*LowLockingEntry)(atomic.LoadPointer(&tab[idx]))
			if e == nil {
				return false
			} else if e == DELETED {
				continue
			} else if e.key == key {
				return true
			}
			idx = (idx + 1) & lenT
		}
	}
	return false
}

func (this *LowLockingSegment) replaceWithOld(key interface{}, hash uint32, oldValue interface{}, newValue interface{}) (replaced bool) {
	tab := this.loadTable()
	lenT := uint32(len(tab) - 1)
	idx := hash & lenT

	//这里不会死循环，因为如果Table太满会触发rehash
	for {
		e := (*LowLockingEntry)(atomic.LoadPointer(&tab[idx]))
		if e == nil {
			return false
		} else if e == DELETED {
			continue
		} else if e.key == key {
			pv := atomic.LoadPointer(&e.value)
			//ABA?
			if *((*interface{})(pv)) == oldValue {
				//如果CAS成功则replace成功，
				//如果CAS失败，说明其他线程已经先更新了value，那么本次replace仍然可以视为成功（相当于replace成功后又被覆盖)
				atomic.CompareAndSwapPointer(&e.value, pv, unsafe.Pointer(&newValue))
				replaced = true
			}
			return
		}
		idx = (idx + 1) & lenT
	}
}

func (this *LowLockingSegment) replace(key interface{}, hash uint32, newValue interface{}) (oldValue interface{}) {
	tab := this.loadTable()
	lenT := uint32(len(tab) - 1)
	idx := hash & lenT

	//这里不会死循环，因为如果Table太满会触发rehash
	for {
		e := (*LowLockingEntry)(atomic.LoadPointer(&tab[idx]))
		if e == nil {
			return false
		} else if e == DELETED {
			continue
		} else if e.key == key {
			return atomic.SwapPointer(&e.value, unsafe.Pointer(&newValue))
		}
		idx = (idx + 1) & lenT
	}
}

/**
 * In Golang中，StorePointer function's asm code includes a xchgl instruction，
 * so it can prevent reorder.
 */
func (this *LowLockingSegment) put(key interface{}, hash uint32, value interface{}, onlyIfAbsent bool) (oldValue interface{}) {
	pTable := atomic.LoadPointer(&this.pTable)
	tab := *(*[]unsafe.Pointer)(pTable)
	lenT := uint32(len(tab) - 1)
	idx := hash & lenT
	var pNewEntry unsafe.Pointer = unsafe.Pointer(&LowLockingEntry{key, hash, unsafe.Pointer(&value), nil})

	c := this.count
	if c > this.threshold { // ensure capacity
		this.rehash()
	}

	//这里不会死循环，因为如果Table太满会触发rehash
	for {
		pe := atomic.LoadPointer(&tab[idx])
		e := (*LowLockingEntry)(pe)
		if e == nil || e == DELETED {
			//if pNewEntry == nil {
			//	pNewEntry = unsafe.Pointer(&LowLockingEntry{key, hash, unsafe.Pointer(&value), nil})
			//}
			//nil和DELETED都表示这个index上没有key-value pair
			oldValue = nil
			if atomic.CompareAndSwapPointer(&tab[idx], pe, pNewEntry) {
				//如果CAS成功，还要判断this.pTable有没有被修改（rehash会修改pTable），
				//如果有的话上面的CAS实际是将值赋到了旧的table中，所以要重新进行put
				if atomic.LoadPointer(&this.pTable) != pTable {
					//重新对新table进行put操作，这里不会导致无限循环，因为扩容后的hashtable又立刻填满的几率很小
					pTable = atomic.LoadPointer(&this.pTable)
					tab = *(*[]unsafe.Pointer)(pTable)
					lenT = uint32(len(tab) - 1)
					idx = hash & lenT
					continue
				} else {
					//put成功，增加count
					atomic.AddInt32(&this.count, 1) //注意这里不能使用局部变量c，因为其他线程可能会修改count
					atomic.AddInt32(this.sumCount, 1)
				}
			}
			//如果CAS失败，说明这个idx已经被其他key-value pair占据，将index++重新进行判断
		} else if e.key == key {
			if !onlyIfAbsent {
				//如果CAS成功则put成功，
				//如果CAS失败，说明其他线程已经先更新了value或者删除了这个节点，那么要从当前位置重新开始判断
				if atomic.CompareAndSwapPointer(&tab[idx], pe, pNewEntry) {
					//如果CAS成功，还要判断this.pTable有没有被修改（rehash会修改pTable），
					//如果有的话上面的CAS实际是将值赋到了旧的table中，所以要重新进行put
					if atomic.LoadPointer(&this.pTable) != pTable {
						//重新对新table进行put操作，这里不会导致无限循环，因为扩容后的hashtable又立刻填满的几率很小
						pTable = atomic.LoadPointer(&this.pTable)
						tab = *(*[]unsafe.Pointer)(pTable)
						lenT = uint32(len(tab) - 1)
						idx = hash & lenT
						continue
					}
					old := atomic.LoadPointer(&e.value)
					oldValue = *(*interface{})(old)
				} else {
					continue
				}

			}
			return
		}
		idx = (idx + 1) & lenT
	}

	//this.lock.Lock()
	//defer this.lock.Unlock()

	//tab := this.table()
	//index := hash & uint32(len(tab)-1)
	//first := (*Entry)(tab[index])
	//e := first
	//for e != nil && (e.hash != hash || key != e.key) {
	//	e = e.next
	//}

	//if e != nil {
	//	oldValue = e.fastValue()
	//	if !onlyIfAbsent {
	//		e.storeValue(&value)
	//	}
	//} else {
	//	oldValue = nil
	//	//this.modCount++
	//	tab[index] = unsafe.Pointer(&Entry{key, hash, unsafe.Pointer(&value), first})
	//	atomic.StoreInt32(&this.count, c) //StoreInt32 can prevent reorder
	//	atomic.AddInt32(this.sumCount, 1)
	//}
	//return
}

/**
 * Remove; match on key only if value nil, else match both.
 */
func (this *LowLockingSegment) remove(key interface{}, hash uint32, value interface{}) (oldValue interface{}) {
	pTable := atomic.LoadPointer(&this.pTable)
	tab := *(*[]unsafe.Pointer)(pTable)
	lenT := uint32(len(tab) - 1)
	idx := hash & lenT

	//这里不会死循环，因为如果Table太满会触发rehash
	for {
		pe := atomic.LoadPointer(&tab[idx])
		e := (*LowLockingEntry)(pe)
		if e == nil {
			return false
		} else if e == DELETED {
			continue
		} else if e.key == key {
			if value != nil {
				pv := atomic.LoadPointer(&e.value)
				//ABA?
				if *((*interface{})(pv)) == value {
					if atomic.CompareAndSwapPointer(&tab[idx], pe, unsafe.Pointer(&DELETED)) {
						//如果CAS成功，还要判断this.pTable有没有被修改（rehash会修改pTable），
						//如果有的话上面的CAS实际是从旧的table中remove，所以要重新进行remove
						if atomic.LoadPointer(&this.pTable) != pTable {
							//重新对新table进行put操作，这里不会导致无限循环，因为扩容后的hashtable又立刻填满的几率很小
							pTable = atomic.LoadPointer(&this.pTable)
							tab = *(*[]unsafe.Pointer)(pTable)
							lenT = uint32(len(tab) - 1)
							idx = hash & lenT
							continue
						} else {
							//remove成功，减少count
							atomic.AddInt32(&this.count, -1) //注意这里不能使用局部变量c，因为其他线程可能会修改count
							atomic.AddInt32(this.sumCount, -1)
							return
						}
					}
					//如果CAS失败，说明这个idx已经被其他key-value pair占据，将index++重新进行判断
				}
			} else {
				if atomic.CompareAndSwapPointer(&tab[idx], pe, unsafe.Pointer(&DELETED)) {
					//如果CAS成功，还要判断this.pTable有没有被修改（rehash会修改pTable），
					//如果有的话上面的CAS实际是从旧的table中remove，所以要重新进行remove
					if atomic.LoadPointer(&this.pTable) != pTable {
						//重新对新table进行put操作，这里不会导致无限循环，因为扩容后的hashtable又立刻填满的几率很小
						pTable = atomic.LoadPointer(&this.pTable)
						tab = *(*[]unsafe.Pointer)(pTable)
						lenT = uint32(len(tab) - 1)
						idx = hash & lenT
						continue
					} else {
						//remove成功，减少count
						atomic.AddInt32(&this.count, -1) //注意这里不能使用局部变量c，因为其他线程可能会修改count
						atomic.AddInt32(this.sumCount, -1)
						return
					}
				}
				//如果CAS失败，说明这个idx已经被其他key-value pair占据，将index++重新进行判断
			}
		}
		idx = (idx + 1) & lenT
	}

	//this.lock.Lock()
	//defer this.lock.Unlock()

	//c := this.count - 1
	//tab := this.table()
	//index := hash & uint32(len(tab)-1)
	//first := (*Entry)(tab[index])
	//e := first

	//for e != nil && (e.hash != hash || key != e.key) {
	//	e = e.next
	//}

	//if e != nil {
	//	v := e.fastValue()
	//	if value == nil || value == v {
	//		oldValue = v
	//		// All entries following removed node can stay
	//		// in list, but all preceding ones need to be
	//		// cloned.
	//		//this.modCount++
	//		newFirst := e.next
	//		for p := first; p != e; p = p.next {
	//			newFirst = &Entry{p.key, p.hash, p.value, newFirst}
	//		}
	//		tab[index] = unsafe.Pointer(newFirst)
	//		atomic.StoreInt32(&this.count, c) //this.count = c
	//		atomic.AddInt32(this.sumCount, -1)
	//	}
	//}
	//return
}

func (this *LowLockingSegment) clear() {
	if count := atomic.LoadInt32(&this.count); count != 0 {
		this.lock.Lock()
		defer this.lock.Unlock()

		tab := this.table()
		for i := 0; i < len(tab); i++ {
			tab[i] = nil
		}
		//this.modCount++
		atomic.StoreInt32(&this.count, 0) //this.count = 0 // write-volatile
		atomic.AddInt32(this.sumCount, -1*count)
	}
}

func (this *LowLockingMap) newSegment(initialCapacity int, lf float32) (s *LowLockingSegment) {
	s = new(LowLockingSegment)
	s.loadFactor = lf
	table := make([]unsafe.Pointer, initialCapacity)
	s.setTable(table)
	s.lock = new(sync.Mutex)
	s.sumCount = &this.count
	return
}

/* ---------------- Iterator Support -------------- */

type LowLockingMapIterator struct {
	nextSegmentIndex int
	nextTableIndex   int
	currentTable     []unsafe.Pointer
	nextEntry        *Entry
	lastReturned     *Entry
	cm               *LowLockingMap
}

func (this *LowLockingMapIterator) advance() {
	if this.nextEntry != nil {
		this.nextEntry = this.nextEntry.next
		if this.nextEntry != nil {
			return
		}
	}

	for this.nextTableIndex >= 0 {
		this.nextEntry = (*Entry)(atomic.LoadPointer(&this.currentTable[this.nextTableIndex]))
		this.nextTableIndex--
		if this.nextEntry != nil {
			return
		}
	}

	for this.nextSegmentIndex >= 0 {
		seg := this.cm.segments[this.nextSegmentIndex]
		this.nextSegmentIndex--
		if atomic.LoadInt32(&seg.count) != 0 {
			this.currentTable = seg.loadTable()
			for j := len(this.currentTable) - 1; j >= 0; j-- {
				this.nextEntry = (*Entry)(atomic.LoadPointer(&this.currentTable[j]))
				if this.nextEntry != nil {
					this.nextTableIndex = j - 1
					return
				}
			}
		}
	}
}

func (this *LowLockingMapIterator) HasNext() bool {
	return this.nextEntry != nil
}

func (this *LowLockingMapIterator) NextEntry() *Entry {
	if this.nextEntry == nil {
		panic(errors.New("NoSuchElementException"))
	}
	this.lastReturned = this.nextEntry
	this.advance()
	return this.lastReturned
}

func (this *LowLockingMapIterator) Remove() {
	if this.lastReturned == nil {
		panic("IllegalStateException")
	}
	this.cm.Remove(this.lastReturned.key)
	this.lastReturned = nil
}

func NewLowLockingMapIterator(cm *LowLockingMap) *LowLockingMapIterator {
	hi := LowLockingMapIterator{}
	hi.nextSegmentIndex = len(cm.segments) - 1
	hi.nextTableIndex = -1
	hi.cm = cm
	hi.advance()
	return &hi
}

func put(table []unsafe.Pointer, entry *LowLockingEntry, onlyIfAbsent bool) (oldValue interface{}) {
	tab := table
	index := entry.hash & uint32(len(tab)-1)
	first := (*LowLockingEntry)(tab[index])
	e := first
	for e != nil && (e.hash != entry.hash || entry.key != e.key) {
		e = e.next
	}

	if e != nil {
		oldValue = e.fastValue()
		if !onlyIfAbsent {
			e.storeValue((*interface{})(entry.value))
		}
	} else {
		oldValue = nil
		//this.modCount++
		tab[index] = unsafe.Pointer(entry)
	}
	return
}